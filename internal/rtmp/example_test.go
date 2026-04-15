package rtmp

import (
	"fmt"
)

func ExampleListen() {
	l, err := Listen("tcp", ":1935")
	if err != nil {
		fmt.Println("listen error")
		return
	}
	defer l.Close()

	fmt.Println(l.Addr().Network())
	// Output:
	// tcp
}
