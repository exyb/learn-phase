package main

import (
	"os"

	"github.com/exyb/go-docker/cmd"
)

func main() {
	if len(os.Args) < 2 {
		panic("参数不足")
	}
	switch os.Args[1] {
	case "run":
		// 默认行为为交互式，除非显式指定 --daemon
		daemon := true
		runArgs := os.Args[2:]
		if len(runArgs) == 0 || runArgs[0] != "--daemon" {
			daemon = false
		} else {
			runArgs = runArgs[1:]
		}
		cmd.RunWithMode(runArgs, daemon)
	case "child":
		cmd.Child()
	case "attach-child":
		cmd.AttachChild()
	case "ps":
		cmd.Ps()
	case "prune":
		cmd.Prune()
	case "top":
		if len(os.Args) < 3 {
			panic("top 需要容器id")
		}
		cmd.Top(os.Args[2])
	case "exec":
		if len(os.Args) < 4 {
			panic("exec 需要容器id和命令")
		}
		cmd.ExecInContainer(os.Args[2], os.Args[3:])
	case "stop":
		if len(os.Args) < 3 {
			panic("stop 需要容器id")
		}
		cmd.StopContainer(os.Args[2])
	case "rm":
		if len(os.Args) < 3 {
			panic("rm 需要容器id")
		}
		cmd.RmContainer(os.Args[2])
	default:
		panic("what?")
	}
}

// 其它命令的实现（如 child、attachChild、ps、prune、top、execInContainer、stopContainer、rmContainer）
// 可继续拆分到 cmd 目录下的对应文件，保持 main.go 只做分发。
