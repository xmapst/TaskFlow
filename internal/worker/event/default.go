package event

import "fmt"

var (
	channel    = make(chan string, defaultBufferSize)
	observable = New[string](channel)
)

func Sendf(format string, args ...any) {
	Send(fmt.Sprintf(format, args...))
}

func Send(event string) {
	select {
	case channel <- event:
	default:
		// 丢弃或处理缓冲区满时的情况
	}
}

// SubscribeSince subscribes from a sequence cursor; 0 means "only new events".
func SubscribeSince(since uint64) (Replay[string], error) {
	return observable.SubscribeSince(since)
}

func Unsubscribe(id int64) {
	observable.Unsubscribe(id)
}
