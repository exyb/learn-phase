package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func Child() {
	// 在新的namespace 运行, 真正做环境隔离
	fmt.Printf("Running %v in child process as container\n", os.Args[2:])
	syscall.Sethostname([]byte("container"))

	rootfs := os.Getenv("CONTAINER_ROOTFS")
	fmt.Printf("child: CONTAINER_ROOTFS=%s\n", rootfs)
	if rootfs == "" {
		rootfs = "/tmp/newroot/"
	}
	// chroot 前调试
	out1, err1 := exec.Command("ls", "-l", rootfs).CombinedOutput()
	fmt.Printf("child: chroot前 ls -l %s 输出:\n%s\nerr: %v\n", rootfs, string(out1), err1)
	err2 := syscall.Chroot(rootfs)
	if err2 != nil {
		fmt.Printf("child: chroot(%s) 失败: %v\n", rootfs, err2)
		panic(err2)
	}
	err3 := os.Chdir("/")
	if err3 != nil {
		fmt.Printf("child: chdir(/) 失败: %v\n", err3)
		panic(err3)
	}
	// chroot 后调试
	out2, err4 := exec.Command("ls", "-l", "/").CombinedOutput()
	fmt.Printf("child: chroot后 ls -l / 输出:\n%s\nerr: %v\n", string(out2), err4)
	// 自动创建 /dev/null 和 /dev/tty 等伪设备
	setupDev()
	// 挂载点设置为私有，防止影响宿主机
	must(syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""))
	// 挂载 /dev/pts，保证伪终端可用
	os.MkdirAll("/dev/pts", 0755)
	must(syscall.Mount("devpts", "/dev/pts", "devpts", 0, ""))
	// 挂载 proc 文件系统，保证 ps/top 等命令可用
	must(syscall.Mount("proc", "/proc", "proc", 0, ""))
	// 设置常用环境变量，提升 shell 交互体验
	os.Setenv("TERM", "xterm")
	os.Setenv("HOME", "/root")
	os.Setenv("USER", "root")
	os.Setenv("PATH", "/bin:/usr/bin:/sbin:/usr/sbin")
	os.Setenv("PS1", "[container \\u@\\h \\w]# ")
	os.Setenv("PROMPT_COMMAND", "")
	os.Setenv("LC_ALL", "C")
	os.Setenv("LANG", "C")

	// 调试：打印 tty 及设备节点信息
	fmt.Println("child: 调试 tty 及设备节点信息")
	out3, err5 := exec.Command("tty").CombinedOutput()
	fmt.Printf("child: tty 命令输出: %s, err: %v\n", string(out3), err5)
	out4, err6 := exec.Command("ls", "-l", "/dev/tty", "/dev/console", "/dev/ptmx", "/dev/pts").CombinedOutput()
	fmt.Printf("child: ls -l /dev/tty /dev/console /dev/ptmx /dev/pts 输出:\n%s\nerr: %v\n", string(out4), err6)
	// 新增更多文件系统调试信息
	out5, err7 := exec.Command("ls", "-lR", "/dev").CombinedOutput()
	fmt.Printf("child: ls -lR /dev 输出:\n%s\nerr: %v\n", string(out5), err7)
	out6, err8 := exec.Command("mount").CombinedOutput()
	fmt.Printf("child: mount 输出:\n%s\nerr: %v\n", string(out6), err8)
	out7, err9 := exec.Command("cat", "/proc/mounts").CombinedOutput()
	fmt.Printf("child: /proc/mounts 输出:\n%s\nerr: %v\n", string(out7), err9)
	out8, err10 := exec.Command("ls", "-l", "/").CombinedOutput()
	fmt.Printf("child: ls -l / 输出:\n%s\nerr: %v\n", string(out8), err10)

	// 保存环境变量到 /tmp/container_<id>.env
	containerID := os.Getenv("CONTAINER_ID")
	if containerID == "" && len(os.Args) > 2 {
		containerID = os.Args[2]
	}
	envFile := "/tmp/container_" + containerID + ".env"
	f, err := os.Create(envFile)
	if err == nil {
		for _, kv := range os.Environ() {
			f.WriteString(kv + "\n")
		}
		f.Close()
	}
	// 执行输入的命令，强制加 -i 参数提升交互性
	cmdArgs := os.Args[2:]
	// if len(cmdArgs) > 0 && (cmdArgs[0] == "/bin/sh" || cmdArgs[0] == "/bin/busybox") {
	// 	cmdArgs = append(cmdArgs, "-i")
	// }
	must(syscall.Exec(cmdArgs[0], cmdArgs, os.Environ()))
}

// 自动创建 /dev/null 和 /dev/tty 等伪设备
func setupDev() {
	os.MkdirAll("/dev", 0755)
	// /dev/null
	if _, err := os.Stat("/dev/null"); os.IsNotExist(err) {
		syscall.Mknod("/dev/null", syscall.S_IFCHR|0666, int((1<<8)|3)) // major=1, minor=3
	}
	// /dev/zero
	if _, err := os.Stat("/dev/zero"); os.IsNotExist(err) {
		syscall.Mknod("/dev/zero", syscall.S_IFCHR|0666, int((1<<8)|5)) // major=1, minor=5
	}
	// /dev/tty
	if _, err := os.Stat("/dev/tty"); os.IsNotExist(err) {
		syscall.Mknod("/dev/tty", syscall.S_IFCHR|0666, int((5<<8)|0)) // major=5, minor=0
	}
	// /dev/console
	if _, err := os.Stat("/dev/console"); os.IsNotExist(err) {
		syscall.Mknod("/dev/console", syscall.S_IFCHR|0600, int((5<<8)|1)) // major=5, minor=1
	}
	// /dev/ptmx（主伪终端设备，major=5, minor=2）
	if _, err := os.Stat("/dev/ptmx"); os.IsNotExist(err) {
		syscall.Mknod("/dev/ptmx", syscall.S_IFCHR|0666, int((5<<8)|2))
	}
	// /dev/pts/ptmx 必须是符号链接到 /dev/ptmx
	os.MkdirAll("/dev/pts", 0755)
	ptmxLink := "/dev/pts/ptmx"
	if _, err := os.Lstat(ptmxLink); os.IsNotExist(err) {
		os.Symlink("/dev/ptmx", ptmxLink)
	}
}

// attach-child: 进入目标容器的 namespace 并 chroot 执行命令
func AttachChild() {
	if len(os.Args) < 4 {
		panic("attach-child 需要容器id和命令")
	}
	id := os.Args[2]
	cmdArgs := os.Args[3:]
	b, err := os.ReadFile("/tmp/container_" + id + ".json")
	if err != nil {
		fmt.Println("找不到容器:", id)
		return
	}
	var info ContainerInfo
	json.Unmarshal(b, &info)
	// 依次进入 mount/uts/ipc/net/pid namespace，忽略 mnt 的 setns 错误（部分内核或主进程可能不支持）
	namespaces := []string{"mnt", "uts", "ipc", "net", "pid"}
	for _, ns := range namespaces {
		fd, err := os.Open(fmt.Sprintf("/proc/%d/ns/%s", info.Pid, ns))
		if err != nil {
			fmt.Printf("attach: 打开 namespace %s 失败: %v\n", ns, err)
			continue
		}
		if err := unix.Setns(int(fd.Fd()), 0); err != nil {
			// 仅 mnt namespace 的 setns 失败时降级为警告，其它 namespace 依然报错
			if ns == "mnt" {
				fmt.Printf("attach: setns %s 失败(已忽略): %v\n", ns, err)
			} else {
				fmt.Printf("attach: setns %s 失败: %v\n", ns, err)
			}
		}
		fd.Close()
	}
	// chroot 到容器 rootfs
	if err := syscall.Chroot(info.Rootfs); err != nil {
		fmt.Printf("attach: chroot 失败: %v\n", err)
		return
	}
	if err := os.Chdir("/"); err != nil {
		fmt.Printf("attach: chdir 失败: %v\n", err)
		return
	}
	// 执行命令
	must(syscall.Exec(cmdArgs[0], cmdArgs, os.Environ()))
}

// must 函数和 ContainerInfo 结构体可复用 cmd/run.go 的实现
