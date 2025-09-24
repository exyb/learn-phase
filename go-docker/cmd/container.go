package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// 进入容器 namespace 并执行命令
func ExecInContainer(idPrefix string, cmdArgs []string) {
	id, err := FindContainerID(idPrefix)
	if err != nil {
		fmt.Println(err)
		return
	}
	b, err := os.ReadFile("/tmp/container_" + id + ".json")
	if err != nil {
		fmt.Println("找不到容器:", idPrefix)
		return
	}
	var info ContainerInfo
	json.Unmarshal(b, &info)

	// 默认用 nsenter 实现 exec attach
	useNsenter := true
	if os.Getenv("EXEC_USE_NSENTER") == "0" {
		useNsenter = false
	}

	if useNsenter {
		// 优先读取 /tmp/container_<id>.env 作为环境变量
		envFile := "/tmp/container_" + id + ".env"
		env := []string{}
		if b, err := os.ReadFile(envFile); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if line != "" {
					env = append(env, line)
				}
			}
		} else {
			// fallback 到 /proc/<pid>/environ
			environPath := fmt.Sprintf("/proc/%d/environ", info.Pid)
			if b, err := os.ReadFile(environPath); err == nil {
				for _, kv := range strings.Split(string(b), "\x00") {
					if kv != "" {
						env = append(env, kv)
					}
				}
			}
		}
		// 构造 nsenter 命令
		nsenterArgs := []string{
			"--target", fmt.Sprintf("%d", info.Pid),
			"--mount", "--uts", "--ipc", "--net", "--pid", "--preserve-credentials",
			"--",
			"chroot", info.Rootfs,
		}
		nsenterArgs = append(nsenterArgs, cmdArgs...)
		cmd := exec.Command("nsenter", nsenterArgs...)
		cmd.Env = env
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("exec: nsenter 失败: %v\n", err)
		}
		return
	}

	// 旧方式：go setns+chroot
	nsList := []string{"mnt", "uts", "ipc", "net", "pid"}
	for _, ns := range nsList {
		fd, err := os.Open(fmt.Sprintf("/proc/%d/ns/%s", info.Pid, ns))
		if err != nil {
			fmt.Printf("exec: 打开 namespace %s 失败: %v\n", ns, err)
			continue
		}
		importUnixSetns(fd.Fd())
		fd.Close()
	}
	if err := syscall.Chroot(info.Rootfs); err != nil {
		fmt.Printf("exec: chroot 失败: %v\n", err)
		return
	}
	if err := os.Chdir("/"); err != nil {
		fmt.Printf("exec: chdir 失败: %v\n", err)
		return
	}
	// 优先读取 /tmp/container_<id>.env 作为环境变量
	envFile := "/tmp/container_" + id + ".env"
	env := []string{}
	if b, err := os.ReadFile(envFile); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if line != "" {
				env = append(env, line)
			}
		}
	} else {
		environPath := fmt.Sprintf("/proc/%d/environ", info.Pid)
		if b, err := os.ReadFile(environPath); err == nil {
			for _, kv := range strings.Split(string(b), "\x00") {
				if kv != "" {
					env = append(env, kv)
				}
			}
		}
	}
	must(syscall.Exec(cmdArgs[0], cmdArgs, env))
}

// 兼容 go1.22+ 的 namespace 切换
func importUnixSetns(fd uintptr) {
	// 推荐在文件顶部 import "golang.org/x/sys/unix"
	// 实现为
	unix.Setns(int(fd), 0)
	// panic("请在文件顶部添加 import \"golang.org/x/sys/unix\"，并将此函数实现为 unix.Setns(int(fd), 0)")
}

// 停止容器（杀死主进程）
func StopContainer(idPrefix string) {
	id, err := FindContainerID(idPrefix)
	if err != nil {
		fmt.Println(err)
		return
	}
	b, err := os.ReadFile("/tmp/container_" + id + ".json")
	if err != nil {
		fmt.Println("找不到容器:", idPrefix)
		return
	}
	var info ContainerInfo
	json.Unmarshal(b, &info)
	if info.Pid > 0 {
		err := syscall.Kill(info.Pid, syscall.SIGKILL)
		if err != nil {
			fmt.Printf("停止容器 %s 失败: %v\n", id, err)
		} else {
			fmt.Printf("已停止容器 %s (pid=%d)\n", id, info.Pid)
		}
	}
}

// 删除容器（清理挂载和元数据）
func RmContainer(idPrefix string) {
	id, err := FindContainerID(idPrefix)
	if err != nil {
		fmt.Println(err)
		return
	}
	b, err := os.ReadFile("/tmp/container_" + id + ".json")
	if err != nil {
		fmt.Println("找不到容器:", idPrefix)
		return
	}
	var info ContainerInfo
	json.Unmarshal(b, &info)
	// 卸载 overlay2
	if info.Rootfs != "" {
		_ = syscallUnmount(info.Rootfs)
	}
	// 删除 json 文件
	os.Remove("/tmp/container_" + id + ".json")
	// 删除 base 目录及所有子目录
	if info.Rootfs != "" {
		base := info.Rootfs
		if len(base) > 7 && base[len(base)-7:] == "/merged" {
			base = base[:len(base)-7]
		}
		os.RemoveAll(base)
	}
	fmt.Printf("已删除容器 %s\n", id)
}
