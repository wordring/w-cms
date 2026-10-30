//go:build !linux

package main

import "errors"

// openDevice は Linux（業務サーバー）でだけ動きます。ほかの OS では組み立てだけ通します（家の Windows で `go build ./...` が通るように）。
func openDevice(path string) (device, error) {
	return nil, errors.New("この道具は Linux でだけ動きます")
}
