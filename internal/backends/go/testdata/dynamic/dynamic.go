// Package dynamic provides a minimal VTA dispatch fixture.
package dynamic

type Runner interface {
	Run()
}

type Worker struct{}

func (Worker) Run() {}

func Call(runner Runner) {
	runner.Run()
}

func Entry() {
	Call(Worker{})
}
