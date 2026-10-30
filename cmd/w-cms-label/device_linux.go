//go:build linux

package main

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// lpDevice は /dev/usb/lp0 のような USB のプリンターの口です。
//
// ⚠ 読み書きは待たない形（O_NONBLOCK）で開き、自分で間を置いてやり直します。待つ形で開くと、プリンターが答えないときに
// 読みがいつまでも戻りません（Go の os.File は機械の口を待ちの仕組みに載せられないことがある）。
type lpDevice struct{ fd int }

func openDevice(path string) (device, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.ENXIO) {
			return nil, errOff
		}
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return &lpDevice{fd: fd}, nil
}

func (d *lpDevice) Write(b []byte, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for len(b) > 0 {
		n, err := syscall.Write(d.fd, b)
		if n > 0 {
			b = b[n:]
		}
		if err == nil || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
			if len(b) > 0 && n <= 0 {
				if time.Now().After(deadline) {
					return errors.New("プリンターが受け取りません（時間切れ）")
				}
				time.Sleep(10 * time.Millisecond)
			}
			continue
		}
		if errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.EIO) {
			return errOff
		}
		return err
	}
	return nil
}

func (d *lpDevice) Read(timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 0, statusLength)
	tmp := make([]byte, statusLength)
	for len(buf) < statusLength {
		n, err := syscall.Read(d.fd, tmp[:statusLength-len(buf)])
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			continue
		}
		if err != nil && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			if errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.EIO) {
				return nil, errOff
			}
			return nil, err
		}
		if time.Now().After(deadline) {
			if len(buf) == 0 {
				return nil, errNoAnswer
			}
			return nil, errors.New("プリンターの答えが途中で切れました")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return buf, nil
}

func (d *lpDevice) Close() error { return syscall.Close(d.fd) }
