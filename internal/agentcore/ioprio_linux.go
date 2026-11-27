package agentcore

import (
	"runtime"
	"syscall"
)

// lowIOPriority põe a thread atual na classe de E/S ociosa (ionice -c3): o disco atende antes qualquer outro programa.
// A goroutine fica presa à thread e, ao terminar, a thread é descartada: a prioridade baixa não vaza para o resto.
func lowIOPriority() {
	runtime.LockOSThread()
	const whoProcess, classIdle, classShift = 1, 3, 13
	_, _, _ = syscall.Syscall(syscall.SYS_IOPRIO_SET, whoProcess, 0, classIdle<<classShift)
}
