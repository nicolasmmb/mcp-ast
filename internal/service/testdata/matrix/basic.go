package sample

import "fmt"

type Util struct {
	N int
}

var Counter = 0

func Helper() int {
	return 1
}

func (u Util) Method() int {
	_ = fmt.Sprintf("%d", Counter)
	return Helper()
}
