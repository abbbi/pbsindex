/*
		Copyright (C) 2026  Michael Ablassmeier <abi@grinser.de>

	    This program is free software: you can redistribute it and/or modify
	    it under the terms of the GNU General Public License as published by
	    the Free Software Foundation, either version 3 of the License, or
	    (at your option) any later version.

	    This program is distributed in the hope that it will be useful,
	    but WITHOUT ANY WARRANTY; without even the implied warranty of
	    MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
	    GNU General Public License for more details.

	    You should have received a copy of the GNU General Public License
	    along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package main

import "io"

var catalogMagic = [8]byte{0x91, 0xFD, 0x60, 0xF9, 0xC4, 0x67, 0x58, 0xD5}
var didxMagic = [8]byte{0x1C, 0x91, 0x4E, 0xA5, 0x19, 0xBA, 0xB3, 0xCD}

var blobMagicUncompressed = [8]byte{66, 171, 56, 7, 190, 131, 112, 161}
var blobMagicCompressed = [8]byte{49, 185, 88, 66, 111, 182, 163, 127}
var blobMagicEncryptedUncompressed = [8]byte{123, 103, 133, 190, 34, 45, 76, 240}
var blobMagicEncryptedCompressed = [8]byte{230, 89, 27, 191, 11, 191, 216, 11}

type entryType byte

const (
	tDir      entryType = 'd'
	tFile     entryType = 'f'
	tSymlink  entryType = 'l'
	tHardlink entryType = 'h'
	tBlock    entryType = 'b'
	tChar     entryType = 'c'
	tFifo     entryType = 'p'
	tSocket   entryType = 's'
)

type dirEntry struct {
	typeTag entryType
	name    []byte
	offset  uint64
	size    uint64
	mtime   int64
}

type catalogReader struct {
	rs     io.ReadSeeker
	closer io.Closer
}

type byteReader struct {
	data []byte
	pos  int
}

type didxEntry struct {
	EndOffset uint64
	Digest    [32]byte
}

type didxFile struct {
	UUID      [16]byte
	CTime     uint64
	IndexSum  [32]byte
	Entries   []didxEntry
	TotalSize uint64
}

type indexedEntry struct {
	Path      string
	Name      string
	EntryType string
	Size      *uint64
	Mtime     *int64
}

type Result struct {
	SnapshotTime string `json:"snapshot_time"`
	HostKey      string `json:"host_key"`
	ArchiveName  string `json:"archive_name"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Mtime        int64  `json:"mtime"`
	Type         string `json:"entry_type"`
}
