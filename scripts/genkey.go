// Command genkey prints a fresh encryption key id and recovery code.
// scripts/soak uses it. It does not write any configuration.
package main

import (
	"fmt"
	"os"

	"github.com/foisalislambd/openbackup/internal/codec"
)

func main() {
	key, err := codec.NewRandomKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(key.ID())
	fmt.Println(key.RecoveryCode())
}
