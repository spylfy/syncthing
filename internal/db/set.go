// Copyright (C) 2014 The Syncthing Authors.
//
// This program is free software: you can redistribute it and/or modify it
// under the terms of the GNU General Public License as published by the Free
// Software Foundation, either version 3 of the License, or (at your option)
// any later version.
//
// This program is distributed in the hope that it will be useful, but WITHOUT
// ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
// FITNESS FOR A PARTICULAR PURPOSE. See the GNU General Public License for
// more details.
//
// You should have received a copy of the GNU General Public License along
// with this program. If not, see <http://www.gnu.org/licenses/>.

// Package db provides a set type to track local/remote files with newness
// checks. We must do a certain amount of normalization in here. We will get
// fed paths with either native or wire-format separators and encodings
// depending on who calls us. We transform paths to wire-format (NFC and
// slashes) on the way to the database, and transform to native format
// (varying separator and encoding) on the way back out.
package db

import (
	"sync"

	"github.com/syncthing/protocol"
	"github.com/syncthing/syncthing/internal/osutil"
	"github.com/syndtr/goleveldb/leveldb"
)

type FileSet struct {
	localVersion map[protocol.DeviceID]uint64
	mu           sync.RWMutex
	folder       string
	db           *leveldb.DB
	fileDB       *FileDB
	blockmap     *BlockMap
}

// FileIntf is the set of methods implemented by both protocol.FileInfo and
// protocol.FileInfoTruncated.
type FileIntf interface {
	Size() int64
	IsDeleted() bool
	IsInvalid() bool
	IsDirectory() bool
	IsSymlink() bool
	HasPermissionBits() bool
}

// The Iterator is called with either a protocol.FileInfo or a
// protocol.FileInfoTruncated (depending on the method) and returns true to
// continue iteration, false to stop.
type Iterator func(f FileIntf) bool

func NewFileSet(folder string, db *FileDB) *FileSet {
	var s = FileSet{
		localVersion: make(map[protocol.DeviceID]uint64),
		folder:       folder,
		fileDB:       db,
		//blockmap:     NewBlockMap(db, folder),
	}

	/*
		ldbCheckGlobals(db, []byte(folder))

		var deviceID protocol.DeviceID
		ldbWithAllFolderTruncated(db, []byte(folder), func(device []byte, f FileInfoTruncated) bool {
			copy(deviceID[:], device)
			if f.LocalVersion > s.localVersion[deviceID] {
				s.localVersion[deviceID] = f.LocalVersion
			}
			lamport.Default.Tick(f.Version)
			return true
		})
	*/
	if debug {
		l.Debugf("loaded localVersion for %q: %#v", folder, s.localVersion)
	}
	clock(s.localVersion[protocol.LocalDeviceID])

	return &s
}

func (s *FileSet) Replace(device protocol.DeviceID, fs []protocol.FileInfo) {
	if debug {
		l.Debugf("%s Replace(%v, [%d])", s.folder, device, len(fs))
	}
	normalizeFilenames(fs)

	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.fileDB.replace(s.folder, device, fs)
	if err != nil {
		panic(err)
	}
}

func (s *FileSet) ReplaceWithDelete(device protocol.DeviceID, fs []protocol.FileInfo) {
	if debug {
		l.Debugf("%s ReplaceWithDelete(%v, [%d])", s.folder, device, len(fs))
	}
	normalizeFilenames(fs)

	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.fileDB.updateWithDelete(s.folder, device, fs)
	if err != nil {
		panic(err)
	}
}

func (s *FileSet) Update(device protocol.DeviceID, fs []protocol.FileInfo) {
	if debug {
		l.Debugf("%s Update(%v, [%d])", s.folder, device, len(fs))
	}
	normalizeFilenames(fs)

	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.fileDB.update(s.folder, device, fs)
	if err != nil {
		panic(err)
	}
}

func (s *FileSet) WithNeed(device protocol.DeviceID, fn Iterator) {
	if debug {
		l.Debugf("%s WithNeed(%v)", s.folder, device)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	s.fileDB.need(s.folder, device, nativeFileIterator(fn))
}

func (s *FileSet) WithNeedTruncated(device protocol.DeviceID, fn Iterator) {
	if debug {
		l.Debugf("%s WithNeedTruncated(%v)", s.folder, device)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	s.fileDB.needTruncated(s.folder, device, nativeFileIterator(fn))
}

func (s *FileSet) WithHave(device protocol.DeviceID, fn Iterator) {
	if debug {
		l.Debugf("%s WithHave(%v)", s.folder, device)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	s.fileDB.have(s.folder, device, nativeFileIterator(fn))
}

func (s *FileSet) WithHaveTruncated(device protocol.DeviceID, fn Iterator) {
	if debug {
		l.Debugf("%s WithHaveTruncated(%v)", s.folder, device)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	s.fileDB.haveTruncated(s.folder, device, nativeFileIterator(fn))
}

func (s *FileSet) WithGlobal(fn Iterator) {
	if debug {
		l.Debugf("%s WithGlobal()", s.folder)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	s.fileDB.global(s.folder, nativeFileIterator(fn))
}

func (s *FileSet) WithGlobalTruncated(fn Iterator) {
	if debug {
		l.Debugf("%s WithGlobalTruncated()", s.folder)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	s.fileDB.globalTruncated(s.folder, nativeFileIterator(fn))
}

func (s *FileSet) Get(device protocol.DeviceID, file string) (protocol.FileInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	f, ok := s.fileDB.get(s.folder, device, osutil.NormalizedFilename(file))
	f.Name = osutil.NativeFilename(f.Name)
	return f, ok
}

func (s *FileSet) GetGlobal(file string) (protocol.FileInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	f, ok := s.fileDB.getGlobal(s.folder, osutil.NormalizedFilename(file))
	f.Name = osutil.NativeFilename(f.Name)
	return f, ok
}

func (s *FileSet) GetGlobalTruncated(file string) (FileInfoTruncated, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	f, ok := s.fileDB.getGlobalTruncated(s.folder, osutil.NormalizedFilename(file))
	f.Name = osutil.NativeFilename(f.Name)
	return f, ok
}

func (s *FileSet) Availability(file string) []protocol.DeviceID {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.fileDB.availability(s.folder, file)
}

func (s *FileSet) LocalVersion(device protocol.DeviceID) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.fileDB.maxID(s.folder, device)
}

// ListFolders returns the folder IDs seen in the database.
func ListFolders(db *leveldb.DB) []string {
	return ldbListFolders(db)
}

// DropFolder clears out all information related to the given folder from the
// database.
func DropFolder(db *leveldb.DB, folder string) {
	ldbDropFolder(db, []byte(folder))
	bm := &BlockMap{
		db:     db,
		folder: folder,
	}
	bm.Drop()
}

func normalizeFilenames(fs []protocol.FileInfo) {
	for i := range fs {
		fs[i].Name = osutil.NormalizedFilename(fs[i].Name)
	}
}

func nativeFileIterator(fn Iterator) Iterator {
	return func(fi FileIntf) bool {
		switch f := fi.(type) {
		case protocol.FileInfo:
			f.Name = osutil.NativeFilename(f.Name)
			return fn(f)
		case FileInfoTruncated:
			f.Name = osutil.NativeFilename(f.Name)
			return fn(f)
		default:
			panic("unknown interface type")
		}
	}
}
