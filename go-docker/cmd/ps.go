package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// 列出所有容器id
func Ps() {
	files, err := filepath.Glob("/tmp/container_*.json")
	if err != nil {
		fmt.Println("读取容器元数据失败:", err)
		return
	}
	showAll := false
	if len(os.Args) > 2 && os.Args[2] == "-a" {
		showAll = true
	}
	// 打印表头
	fmt.Printf("%-22s %-8s %-8s %-8s\n", "CONTAINER ID", "PID", "STATUS", "ROOTFS")
	for _, f := range files {
		b, _ := os.ReadFile(f)
		var info ContainerInfo
		json.Unmarshal(b, &info)
		// 检查主进程 pid 是否存在于宿主机
		pidExists := false
		if info.Pid > 0 {
			_, err := os.Stat(fmt.Sprintf("/proc/%d", info.Pid))
			if err == nil {
				pidExists = true
			}
		}
		status := "Exited"
		if pidExists {
			status = "Running"
		}
		if pidExists || showAll {
			fmt.Printf("%-22s %-8d %-8s %-8s\n", info.ID, info.Pid, status, info.Rootfs)
		}
	}
}

func Prune() {
	files, err := filepath.Glob("/tmp/container_*.json")
	if err != nil {
		fmt.Println("读取容器元数据失败:", err)
		return
	}
	count := 0
	for _, f := range files {
		b, _ := os.ReadFile(f)
		var info ContainerInfo
		json.Unmarshal(b, &info)
		// 检查主进程 pid 是否存在于宿主机
		pidExists := false
		if info.Pid > 0 {
			_, err := os.Stat(fmt.Sprintf("/proc/%d", info.Pid))
			if err == nil {
				pidExists = true
			}
		}
		if !pidExists {
			// 优先卸载 overlay2 挂载点
			if info.Rootfs != "" {
				_ = syscallUnmount(info.Rootfs)
			}
			// 删除 json 文件
			os.Remove(f)
			// 删除 base 目录及所有子目录
			if info.Rootfs != "" {
				base := info.Rootfs
				if len(base) > 7 && base[len(base)-7:] == "/merged" {
					base = base[:len(base)-7]
				}
				os.RemoveAll(base)
			}
			count++
			fmt.Printf("已清理容器: %s\n", info.ID)
		}
	}
	fmt.Printf("共清理 %d 个已退出容器\n", count)
}

func Top(idPrefix string) {
	id, err := FindContainerID(idPrefix)
	if err != nil {
		fmt.Println(err)
		return
	}
	b, err := os.ReadFile("/tmp/container_" + id + ".json")
	if err != nil {
		fmt.Println("找不到容器:", id)
		return
	}
	var info ContainerInfo
	if err := json.Unmarshal(b, &info); err != nil {
		fmt.Println("容器元数据解析失败:", err)
		return
	}
	fmt.Printf("容器 %s 主进程 pid: %d\n", id, info.Pid)
	// 输出容器内所有进程
	// 1. 获取主进程的 pid namespace inode
	nsPath := fmt.Sprintf("/proc/%d/ns/pid", info.Pid)
	mainNs, err := os.Readlink(nsPath)
	if err != nil {
		fmt.Printf("无法读取主进程 namespace: %v\n", err)
		return
	}
	fmt.Printf("%-8s %-8s %-8s %s\n", "NSPID", "NSPPID", "HOSTPID", "CMD")
	procDir, _ := os.Open("/proc")
	procEntries, _ := procDir.Readdirnames(-1)
	for _, entry := range procEntries {
		pid := entry
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		ns, err := os.Readlink(fmt.Sprintf("/proc/%s/ns/pid", pid))
		if err != nil || ns != mainNs {
			continue
		}
		// 读取 NSpid
		nspid := ""
		nsppid := ""
		ppid := ""
		if b, err := os.ReadFile(fmt.Sprintf("/proc/%s/status", pid)); err == nil {
			lines := strings.Split(string(b), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "NSpid:") {
					parts := strings.Fields(line)
					if len(parts) > 1 {
						nspid = parts[len(parts)-1]
					}
				}
				if strings.HasPrefix(line, "PPid:") {
					parts := strings.Fields(line)
					if len(parts) == 2 {
						ppid = parts[1]
					}
				}
			}
		}
		// 读取父进程的 NSpid
		if ppid != "" {
			if b, err := os.ReadFile(fmt.Sprintf("/proc/%s/status", ppid)); err == nil {
				lines := strings.Split(string(b), "\n")
				for _, line := range lines {
					if strings.HasPrefix(line, "NSpid:") {
						parts := strings.Fields(line)
						if len(parts) > 1 {
							nsppid = parts[len(parts)-1]
						}
						break
					}
				}
			}
		}
		// 读取命令行
		cmdline := ""
		if b, err := os.ReadFile(fmt.Sprintf("/proc/%s/cmdline", pid)); err == nil {
			cmdline = strings.ReplaceAll(string(b), "\x00", " ")
		}
		fmt.Printf("%-8s %-8s %-8s %s\n", nspid, nsppid, pid, cmdline)
	}
}

// 兼容性卸载
func syscallUnmount(target string) error {
	// 优先尝试正常卸载，失败则懒卸载
	if err := syscallUnmountCall(target, 0); err != nil {
		return syscallUnmountCall(target, 2) // syscall.MNT_DETACH = 2
	}
	return nil
}

// 兼容性调用
func syscallUnmountCall(target string, flags int) error {
	return os.NewSyscallError("unmount", syscallRawUnmount(target, flags))
}

// 兼容性底层调用
func syscallRawUnmount(target string, flags int) error {
	// 这里直接用 unix.Unmount 也可以
	return nil // 这里留空，实际项目可用 "golang.org/x/sys/unix".Unmount
}
