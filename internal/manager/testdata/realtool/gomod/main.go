// Command realtool is a fixture for ripple's real-tool tests: it uses its one dependency.
package main

import (
	"fmt"

	"github.com/google/uuid"
)

func main() {
	fmt.Println(uuid.NewString())
}
