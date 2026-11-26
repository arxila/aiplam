// =========================================================================
//
//   Copyright (c) 2026 Arxila OSS (https://arxila.io)
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
//   implied. See the License for the specific language governing
//   permissions and limitations under the License.
//
// =========================================================================

//go:build windows

package fsutil

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const fsctlSetReparsePoint = 0x000900A4

// createLink creates a link to a folder as a directory junction, and a link to a file as a symbolic link: junctions
// cannot point to files, and symbolic links need developer mode (or administrator rights).
func createLink(link, target string) error {
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		return createFileLink(link, target)
	}
	return createJunction(link, target)
}

func restoreLink(link, target string, symlink bool) error {
	if _, err := os.Stat(target); err != nil && symlink {
		return createFileLink(link, target)
	}
	return createLink(link, target)
}

func fileLinksAllowed() bool {
	dir, err := os.MkdirTemp("", "aiplam-")
	if err != nil {
		return true
	}
	defer os.RemoveAll(dir)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		return true
	}
	err = os.Symlink("target", filepath.Join(dir, "link"))
	return !errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD)
}

// createFileLink creates a symbolic link to a file, with a target relative to the link's folder when possible.
func createFileLink(link, target string) error {
	if rel, err := filepath.Rel(Canonical(filepath.Dir(link)), linkTarget(target)); err == nil {
		target = rel
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("%w (links to files, such as subagents or commands, are symbolic links, which on Windows "+
			"need developer mode or administrator rights)", err)
	}
	return nil
}

// createJunction creates a directory junction (an NTFS mount point reparse point), which, unlike a symbolic link,
// needs neither administrator rights nor developer mode. It does what "mklink /J" does, without going through cmd.exe.
func createJunction(link, target string) (err error) {
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.Mkdir(link, 0o777); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(link)
		}
	}()

	name, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return &os.PathError{Op: "open", Path: link, Err: err}
	}
	defer windows.CloseHandle(handle)

	buf := mountPointReparseBuffer(target)
	var returned uint32
	if err := windows.DeviceIoControl(handle, fsctlSetReparsePoint, &buf[0], uint32(len(buf)), nil, 0, &returned,
		nil); err != nil {
		return fmt.Errorf("creating junction %s -> %s: %w", link, target, err)
	}
	return nil
}

// mountPointReparseBuffer builds a REPARSE_DATA_BUFFER for IO_REPARSE_TAG_MOUNT_POINT: the header (tag, data length,
// reserved), the MountPointReparseBuffer offsets and lengths, and the path buffer with the NUL-terminated substitute
// name ("\??\C:\target") followed by the NUL-terminated print name ("C:\target").
func mountPointReparseBuffer(target string) []byte {
	substitute := utf16.Encode([]rune(`\??\` + target))
	printName := utf16.Encode([]rune(target))
	path := make([]uint16, 0, len(substitute)+len(printName)+2)
	path = append(path, substitute...)
	path = append(path, 0)
	path = append(path, printName...)
	path = append(path, 0)

	dataLength := 8 + 2*len(path)
	buf := make([]byte, 8+dataLength)
	le := binary.LittleEndian
	le.PutUint32(buf[0:], windows.IO_REPARSE_TAG_MOUNT_POINT)
	le.PutUint16(buf[4:], uint16(dataLength))
	le.PutUint16(buf[6:], 0)
	le.PutUint16(buf[8:], 0)                            // SubstituteNameOffset
	le.PutUint16(buf[10:], uint16(2*len(substitute)))   // SubstituteNameLength
	le.PutUint16(buf[12:], uint16(2*len(substitute)+2)) // PrintNameOffset
	le.PutUint16(buf[14:], uint16(2*len(printName)))    // PrintNameLength
	for i, c := range path {
		le.PutUint16(buf[16+2*i:], c)
	}
	return buf
}
