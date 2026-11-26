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

package fsutil

import (
	"errors"
	"os"
)

// ErrLocked is returned by Lock when another process holds the lock.
var ErrLocked = errors.New("locked by another process")

// Lock takes an exclusive lock on the file at path, creating it when needed, without waiting: it fails with ErrLocked
// when another process holds it. The operating system releases the lock when the process ends, even if it crashes, so
// a lock is never left behind. The returned function releases it; the file itself is kept. It fails when something
// else than a regular file is at path, such as a link, which it never follows.
func Lock(path string) (unlock func(), err error) {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return nil, &os.PathError{Op: "lock", Path: path, Err: errors.New("not a regular file")}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = unlockFile(f)
		f.Close()
	}, nil
}
