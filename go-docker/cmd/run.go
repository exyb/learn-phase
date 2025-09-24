package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func RunWithMode(args []string, daemon bool) {
	if len(args) < 2 {
		panic("run 需要指定镜像tag和命令，例如 run alpine:3.18 /bin/sh")
	}
	imageTag := args[0]
	cmdArgs := args[1:]

	// 1. 解析 manifest.json，找到对应 layer tar
	layerTar := ""
	manifestFile := "unpack/manifest.json"
	b, err := os.ReadFile(manifestFile)
	must(err)
	var manifests []map[string]interface{}
	must(json.Unmarshal(b, &manifests))
	for _, m := range manifests {
		tags, ok := m["RepoTags"].([]interface{})
		if !ok || tags == nil {
			continue
		}
		for _, t := range tags {
			if t.(string) == imageTag {
				layers := m["Layers"].([]interface{})
				layerTar = layers[0].(string)
				break
			}
		}
	}
	if layerTar == "" {
		panic("未找到镜像层: " + imageTag)
	}

	// 2. 创建 overlay2 目录结构
	cid := genContainerID()
	base := "/tmp/container_" + cid
	lowerdir := base + "/lower"
	upperdir := base + "/upper"
	workdir := base + "/work"
	merged := base + "/merged"
	must(os.MkdirAll(lowerdir, 0755))
	must(os.MkdirAll(upperdir, 0755))
	must(os.MkdirAll(workdir, 0755))
	must(os.MkdirAll(merged, 0755))

	// 3. 解包 layer tar 到 lowerdir
	tarPath := layerTar
	if !strings.HasPrefix(tarPath, "unpack/") {
		tarPath = "unpack/" + tarPath
	}
	fmt.Printf("解包镜像层 %s 到 %s\n", tarPath, lowerdir)
	must(exec.Command("tar", "-xf", tarPath, "-C", lowerdir).Run())

	// 4. 挂载 overlay2
	fmt.Printf("挂载 overlay2 到 %s\n", merged)
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerdir, upperdir, workdir)
	must(syscall.Mount("overlay", merged, "overlay", 0, opts))

	// 5. 启动容器进程
	fmt.Printf("启动容器 %s，命令: %v\n", cid, cmdArgs)

	selfExe, err := filepath.Abs(os.Args[0])
	must(err)
	// 执行child命令
	childCmd := exec.Command(selfExe, append([]string{"child"}, cmdArgs...)...)
	childCmd.Env = append(os.Environ(), "CONTAINER_ROOTFS="+merged)
	childCmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
	}
	if !daemon {
		// 使用 pty 分配伪终端，保证容器内 shell 交互
		// 优化：在启动 child 进程前同步窗口大小，确保 shell 能正确获取尺寸
		// daemon模式分配pty，并设为控制终端
		winSize, _ := unix.IoctlGetWinsize(int(os.Stdin.Fd()), unix.TIOCGWINSZ)
		ptmx, err := pty.StartWithAttrs(childCmd, &pty.Winsize{
			Rows: winSize.Row,
			Cols: winSize.Col,
			X:    winSize.Xpixel,
			Y:    winSize.Ypixel,
		}, &syscall.SysProcAttr{
			Setsid:     true,
			Setctty:    true,
			Ctty:       0, // 由 pty.StartWithAttrs 自动设置
			Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
		})
		must(err)
		// daemon模式无需同步窗口大小和信号
		// 立即记录容器元数据（此时 child 进程已启动，pid 已分配）
		info := ContainerInfo{
			ID:     cid,
			Rootfs: merged,
			Pid:    childCmd.Process.Pid,
		}
		saveContainerInfo(info)
		fmt.Printf("容器启动成功，id: %s, pid: %d\n", cid, info.Pid)
		go func() { _, _ = io.Copy(os.Stdout, ptmx) }()
		go func() { _, _ = io.Copy(ptmx, os.Stdin) }()
		fmt.Println("runWithMode: 等待 child 进程退出 ...")
		err = childCmd.Wait()
		fmt.Printf("runWithMode: child 进程退出，err=%v\n", err)
		ptmx.Close()
		// 容器进程退出后，自动清理 overlay2 挂载和目录
		// 优先尝试正常卸载，失败则懒卸载
		if err := syscall.Unmount(merged, 0); err != nil {
			// 懒卸载，确保挂载点被清理
			syscall.Unmount(merged, syscall.MNT_DETACH)
		}
		base := strings.TrimSuffix(merged, "/merged")
		os.RemoveAll(base)
	} else {
		// daemon 模式也分配 pty，保证 /bin/sh 检测到 tty 不会立即退出
		ptmx, err := ptyStart(childCmd)
		must(err)
		// 立即记录容器元数据
		info := ContainerInfo{
			ID:     cid,
			Rootfs: merged,
			Pid:    childCmd.Process.Pid,
		}
		saveContainerInfo(info)
		fmt.Printf("runWithMode: daemon 模式 child 启动，err=%v\n", err)
		fmt.Printf("容器启动成功，id: %s, pid: %d\n", cid, info.Pid)
		fmt.Println("容器已在后台运行。请使用如下命令进入容器终端：")
		fmt.Printf("./go-docker exec %s /bin/sh\n", cid)
		// 后台运行，持有 pty，防止 shell 检测到 EOF/SIGHUP 自动退出
		go func() {
			// 持续读写，防止 pty 被关闭
			buf := make([]byte, 1024)
			for {
				_, err := ptmx.Read(buf)
				if err != nil {
					break
				}
			}
		}()
		// 不要关闭 ptmx，保持 pty 句柄直到主进程退出
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func genContainerID() string {
	b := make([]byte, 6) // 12 hex chars
	_, err := rand.Read(b)
	if err != nil {
		// fallback
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// 通过前缀查找唯一容器ID
func FindContainerID(prefix string) (string, error) {
	files, err := filepath.Glob("/tmp/container_*.json")
	if err != nil {
		return "", fmt.Errorf("读取容器元数据失败: %v", err)
	}
	var match string
	for _, f := range files {
		id := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(f), ".json"), "container_")
		if strings.HasPrefix(id, prefix) {
			if match != "" {
				return "", fmt.Errorf("前缀 %s 匹配多个容器ID", prefix)
			}
			match = id
		}
	}
	if match == "" {
		return "", fmt.Errorf("未找到匹配的容器ID: %s", prefix)
	}
	return match, nil
}

func saveContainerInfo(info ContainerInfo) {
	f, err := os.Create("/tmp/container_" + info.ID + ".json")
	if err != nil {
		fmt.Println("保存容器元数据失败:", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "{\"id\":\"%s\",\"rootfs\":\"%s\",\"pid\":%d}\n", info.ID, info.Rootfs, info.Pid)
}

func ptyStart(cmd *exec.Cmd) (*os.File, error) {
	return pty.Start(cmd)
}
