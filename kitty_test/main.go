package main

import (
	"fmt"

	"github.com/blacktop/go-termimg"
)

func main() {
	protocol := termimg.DetectProtocol()
	fmt.Println(protocol)
}
