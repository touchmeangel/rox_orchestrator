package dockerx

type Suspendable interface {
	Suspend()
	Resume()
}
