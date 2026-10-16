//go:build !linux

package gigaam

import "fmt"

func checkCUDA() error {
	return fmt.Errorf("CUDA requested: this project packages CUDA for Linux amd64; use Dockerfile.gpu")
}
