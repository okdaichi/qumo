package rtmp

import (
	"fmt"

	"github.com/okdaichi/qumo/internal/rtmp/amf0"
	"github.com/okdaichi/qumo/internal/rtmp/amf3"
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

func Example_amf0MarshalUnmarshal() {
	payload := map[string]any{
		"cmd": "connect",
		"txn": float64(1),
	}

	b, err := amf0.Marshal(payload)
	if err != nil {
		fmt.Println("marshal error")
		return
	}

	v, err := amf0.Unmarshal(b)
	if err != nil {
		fmt.Println("unmarshal error")
		return
	}

	m, ok := v.(map[string]any)
	if !ok {
		fmt.Println("unexpected type")
		return
	}

	fmt.Printf("%s %.0f\n", m["cmd"], m["txn"])
	// Output:
	// connect 1
}

func Example_amf3MarshalUnmarshal() {
	payload := amf3.Array{
		Associative: map[string]any{"type": "meta"},
		Dense:       []any{int32(7), "ok"},
	}

	b, err := amf3.Marshal(payload)
	if err != nil {
		fmt.Println("marshal error")
		return
	}

	v, err := amf3.Unmarshal(b)
	if err != nil {
		fmt.Println("unmarshal error")
		return
	}

	a, ok := v.(amf3.Array)
	if !ok {
		fmt.Println("unexpected type")
		return
	}

	fmt.Printf("%s %d %s\n", a.Associative["type"], a.Dense[0], a.Dense[1])
	// Output:
	// meta 7 ok
}
