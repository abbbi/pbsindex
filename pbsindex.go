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

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

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

func newCatalogReader(path string) (*catalogReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &catalogReader{rs: f, closer: f}, nil
}

func newCatalogReaderFromBytes(data []byte) *catalogReader {
	return &catalogReader{rs: bytes.NewReader(data)}
}

func (r *catalogReader) close() error {
	if r.closer == nil {
		return nil
	}
	return r.closer.Close()
}

func (r *catalogReader) rootStart() (uint64, error) {
	if _, err := r.rs.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}

	var magic [8]byte
	if _, err := io.ReadFull(r.rs, magic[:]); err != nil {
		return 0, err
	}
	if magic != catalogMagic {
		return 0, fmt.Errorf("unexpected catalog magic: %x", magic)
	}

	if _, err := r.rs.Seek(-8, io.SeekEnd); err != nil {
		return 0, err
	}
	var start uint64
	if err := binary.Read(r.rs, binary.LittleEndian, &start); err != nil {
		return 0, err
	}
	return start, nil
}

func decodeU64(rd io.ByteReader) (uint64, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		b, err := rd.ReadByte()
		if err != nil {
			return 0, err
		}
		if b < 128 {
			v |= uint64(b) << (i * 7)
			return v, nil
		}
		v |= uint64(b&127) << (i * 7)
	}
	return 0, errors.New("decode_u64 failed: missing end marker")
}

func decodeI64(rd io.ByteReader) (int64, error) {
	var v uint64
	for i := 0; i < 11; i++ {
		b, err := rd.ReadByte()
		if err != nil {
			return 0, err
		}

		switch {
		case b == 0:
			if v == 0 {
				return 0, nil
			}
			return ((int64(v) - 1) * -1) - 1, nil
		case b < 128:
			v |= uint64(b) << (i * 7)
			return int64(v), nil
		default:
			v |= uint64(b&127) << (i * 7)
		}
	}
	return 0, errors.New("decode_i64 failed: missing end marker")
}

func decodeU64FromReader(rd io.Reader) (uint64, error) {
	var v uint64
	var b [1]byte
	for i := 0; i < 10; i++ {
		if _, err := io.ReadFull(rd, b[:]); err != nil {
			return 0, err
		}
		t := b[0]
		if t < 128 {
			v |= uint64(t) << (i * 7)
			return v, nil
		}
		v |= uint64(t&127) << (i * 7)
	}
	return 0, errors.New("decode_u64 failed: missing end marker")
}

func (r *catalogReader) readRawDirBlock(start uint64) ([]byte, error) {
	if _, err := r.rs.Seek(int64(start), io.SeekStart); err != nil {
		return nil, err
	}

	size, err := decodeU64FromReader(r.rs)
	if err != nil {
		return nil, err
	}
	if size < 1 {
		return nil, fmt.Errorf("invalid directory block size: %d", size)
	}

	data := make([]byte, size)
	if _, err := io.ReadFull(r.rs, data); err != nil {
		return nil, err
	}
	return data, nil
}

func parseDirEntries(data []byte) ([]dirEntry, error) {
	r := &byteReader{data: data}
	count, err := decodeU64(r)
	if err != nil {
		return nil, err
	}

	entries := make([]dirEntry, 0, count)
	for i := uint64(0); i < count; i++ {
		t, err := r.ReadByte()
		if err != nil {
			return nil, err
		}

		nameLen, err := decodeU64(r)
		if err != nil {
			return nil, err
		}
		if nameLen > 4095 {
			return nil, fmt.Errorf("directory entry name too long: %d", nameLen)
		}
		name := make([]byte, nameLen)
		if _, err := io.ReadFull(r, name); err != nil {
			return nil, err
		}

		e := dirEntry{typeTag: entryType(t), name: name}
		switch e.typeTag {
		case tDir:
			e.offset, err = decodeU64(r)
			if err != nil {
				return nil, err
			}
		case tFile:
			e.size, err = decodeU64(r)
			if err != nil {
				return nil, err
			}
			e.mtime, err = decodeI64(r)
			if err != nil {
				return nil, err
			}
		case tSymlink, tHardlink, tBlock, tChar, tFifo, tSocket:
			// no extra data
		default:
			return nil, fmt.Errorf("invalid catalog entry type: %q", byte(e.typeTag))
		}

		entries = append(entries, e)
	}

	if r.remaining() != 0 {
		return nil, fmt.Errorf("unparsed bytes left in dir block: %d", r.remaining())
	}

	return entries, nil
}

func (r *catalogReader) dumpDir(prefix string, start uint64) error {
	data, err := r.readRawDirBlock(start)
	if err != nil {
		return err
	}

	entries, err := parseDirEntries(data)
	if err != nil {
		return err
	}

	for _, e := range entries {
		full := filepath.Join(prefix, string(e.name))
		display := filepath.ToSlash(full)
		if !strings.HasPrefix(display, "./") {
			display = "./" + display
		}

		switch e.typeTag {
		case tDir:
			fmt.Printf("%c %s\n", e.typeTag, display)
			if e.offset > start {
				return fmt.Errorf("invalid directory offset %d at %s (start=%d)", e.offset, display, start)
			}
			childStart := start - e.offset
			if err := r.dumpDir(full, childStart); err != nil {
				return err
			}
		case tFile:
			mtime := time.Unix(e.mtime, 0).UTC().Format(time.RFC3339)
			fmt.Printf("%c %s size=%d mtime=%s\n", e.typeTag, display, e.size, mtime)
		default:
			fmt.Printf("%c %s\n", e.typeTag, display)
		}
	}

	return nil
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (r *byteReader) ReadByte() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func (r *byteReader) remaining() int {
	return len(r.data) - r.pos
}

func parseDidx(path string) (*didxFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 4096 {
		return nil, fmt.Errorf("didx too small: %d", len(data))
	}

	hdr := data[:4096]
	var magic [8]byte
	copy(magic[:], hdr[:8])
	if magic != didxMagic {
		return nil, fmt.Errorf("unexpected didx magic: %x", magic)
	}

	out := &didxFile{}
	copy(out.UUID[:], hdr[8:24])
	out.CTime = binary.LittleEndian.Uint64(hdr[24:32])
	copy(out.IndexSum[:], hdr[32:64])

	indexData := data[4096:]
	sum := sha256.Sum256(indexData)
	if !bytes.Equal(sum[:], out.IndexSum[:]) {
		return nil, fmt.Errorf("didx index checksum mismatch: expected=%x got=%x", out.IndexSum, sum)
	}

	if len(indexData)%40 != 0 {
		return nil, fmt.Errorf("invalid didx index size: %d", len(indexData))
	}

	count := len(indexData) / 40
	out.Entries = make([]didxEntry, 0, count)

	prev := uint64(0)
	for i := 0; i < count; i++ {
		off := i * 40
		end := binary.LittleEndian.Uint64(indexData[off : off+8])
		if end < prev {
			return nil, fmt.Errorf("non-monotonic end offset at entry %d", i)
		}
		var digest [32]byte
		copy(digest[:], indexData[off+8:off+40])
		out.Entries = append(out.Entries, didxEntry{EndOffset: end, Digest: digest})
		prev = end
	}
	out.TotalSize = prev

	return out, nil
}

func formatUUID(b [16]byte) string {
	hexStr := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:32])
}

func printDidxIndex(df *didxFile) {
	fmt.Printf("didx uuid=%s ctime=%s chunks=%d total_size=%d\n", formatUUID(df.UUID), time.Unix(int64(df.CTime), 0).UTC().Format(time.RFC3339), len(df.Entries), df.TotalSize)
	prev := uint64(0)
	for i, e := range df.Entries {
		chunkSize := e.EndOffset - prev
		fmt.Printf("chunk[%d] start=%d end=%d size=%d digest=%x\n", i, prev, e.EndOffset, chunkSize, e.Digest)
		prev = e.EndOffset
	}
}

func zstdDecompress(in []byte) ([]byte, error) {
	cmd := exec.Command("zstd", "-d", "-q", "-c")
	cmd.Stdin = bytes.NewReader(in)
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("zstd decompress failed: %s", msg)
	}
	return out.Bytes(), nil
}

func decodeChunkBlob(data []byte) ([]byte, error) {
	if len(data) < 12 {
		return data, nil
	}

	var magic [8]byte
	copy(magic[:], data[:8])
	crcWant := binary.LittleEndian.Uint32(data[8:12])
	payload := data[12:]

	switch magic {
	case blobMagicUncompressed:
		_ = crcWant
		_ = crc32.ChecksumIEEE(payload)
		return payload, nil
	case blobMagicCompressed:
		plain, err := zstdDecompress(payload)
		if err != nil {
			return nil, err
		}
		_ = crcWant
		_ = crc32.ChecksumIEEE(plain)
		return plain, nil
	case blobMagicEncryptedUncompressed, blobMagicEncryptedCompressed:
		return nil, errors.New("encrypted chunk blob: decryption key support is not implemented")
	default:
		return data, nil
	}
}

func readAndVerifyChunk(path string, want [32]byte) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	decoded, err := decodeChunkBlob(data)
	if err != nil {
		return nil, false, fmt.Errorf("cannot decode chunk blob %s: %w", path, err)
	}

	sum := sha256.Sum256(decoded)
	if !bytes.Equal(sum[:], want[:]) {
		return nil, false, nil
	}
	return decoded, true, nil
}

func chunkCandidates(chunkDir string, digest [32]byte) []string {
	hexDigest := hex.EncodeToString(digest[:])
	candidates := []string{filepath.Join(chunkDir, hexDigest)}
	if len(hexDigest) >= 4 {
		candidates = append(candidates, filepath.Join(chunkDir, hexDigest[0:4], hexDigest))
	}
	if len(hexDigest) >= 3 {
		candidates = append(candidates, filepath.Join(chunkDir, hexDigest[0:2], hexDigest[2:]))
	}
	if len(hexDigest) >= 5 {
		candidates = append(candidates, filepath.Join(chunkDir, hexDigest[0:2], hexDigest[2:4], hexDigest[4:]))
	}
	return candidates
}

func reconstructCatalogFromDidx(path string, df *didxFile, chunkDir string) ([]byte, error) {
	basePath := strings.TrimSuffix(path, ".didx")
	dir := filepath.Dir(path)
	if chunkDir == "" {
		chunkDir = dir
	}

	out := make([]byte, 0, df.TotalSize)
	prev := uint64(0)

	for i, e := range df.Entries {
		chunkSize := e.EndOffset - prev
		if chunkSize == 0 {
			prev = e.EndOffset
			continue
		}

		candidates := chunkCandidates(chunkDir, e.Digest)
		if len(df.Entries) == 1 && i == 0 {
			candidates = append([]string{basePath}, candidates...)
		}

		var chunk []byte
		found := false
		for _, candidate := range candidates {
			data, ok, err := readAndVerifyChunk(candidate, e.Digest)
			if err != nil {
				return nil, err
			}
			if ok {
				chunk = data
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("missing chunk %d digest=%x (checked %d candidate paths)", i, e.Digest, len(candidates))
		}

		if uint64(len(chunk)) < chunkSize {
			return nil, fmt.Errorf("chunk %d too small: got=%d need>=%d", i, len(chunk), chunkSize)
		}
		out = append(out, chunk[:chunkSize]...)
		prev = e.EndOffset
	}

	return out, nil
}

func dumpCatalogFromReader(r *catalogReader) error {
	root, err := r.rootStart()
	if err != nil {
		return fmt.Errorf("catalog header error: %w", err)
	}
	if err := r.dumpDir(".", root); err != nil {
		return fmt.Errorf("dump error: %w", err)
	}
	return nil
}

func collectCatalogEntries(data []byte) ([]indexedEntry, error) {
	r := newCatalogReaderFromBytes(data)
	root, err := r.rootStart()
	if err != nil {
		return nil, fmt.Errorf("catalog header error: %w", err)
	}
	return r.collectDir("/", root)
}

func (r *catalogReader) collectDir(prefix string, start uint64) ([]indexedEntry, error) {
	data, err := r.readRawDirBlock(start)
	if err != nil {
		return nil, err
	}

	entries, err := parseDirEntries(data)
	if err != nil {
		return nil, err
	}

	out := make([]indexedEntry, 0, len(entries))
	for _, e := range entries {
		full := filepath.ToSlash(filepath.Join(prefix, string(e.name)))
		if !strings.HasPrefix(full, "/") {
			full = "/" + full
		}

		row := indexedEntry{
			Path:      full,
			Name:      string(e.name),
			EntryType: string([]byte{byte(e.typeTag)}),
		}
		if e.typeTag == tFile {
			size := e.size
			mtime := e.mtime
			row.Size = &size
			row.Mtime = &mtime
		}
		out = append(out, row)

		if e.typeTag == tDir {
			if e.offset > start {
				return nil, fmt.Errorf("invalid directory offset %d at %s (start=%d)", e.offset, full, start)
			}
			childStart := start - e.offset
			childRows, err := r.collectDir(full, childStart)
			if err != nil {
				return nil, err
			}
			out = append(out, childRows...)
		}
	}

	return out, nil
}

func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func runSQLiteExec(dbPath, sql string) error {
	cmd := exec.Command("sqlite3", dbPath)
	cmd.Stdin = strings.NewReader(sql)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

func runSQLiteQuery(dbPath, sql string) (string, error) {
	cmd := exec.Command("sqlite3", "-noheader", "-separator", "\t", dbPath, sql)
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return out.String(), nil
}

func ensureSchema(dbPath string) error {
	schema := `
CREATE TABLE IF NOT EXISTS host (
  id INTEGER PRIMARY KEY,
  host_key TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS snapshot (
  id INTEGER PRIMARY KEY,
  host_id INTEGER NOT NULL REFERENCES host(id),
  snapshot_time TEXT NOT NULL,
  backup_type TEXT,
  backup_id TEXT,
  UNIQUE(host_id, snapshot_time)
);
CREATE TABLE IF NOT EXISTS archive (
  id INTEGER PRIMARY KEY,
  snapshot_id INTEGER NOT NULL REFERENCES snapshot(id),
  didx_uuid TEXT NOT NULL UNIQUE,
  archive_name TEXT NOT NULL,
  catalog_didx_path TEXT NOT NULL,
  ctime TEXT,
  chunk_count INTEGER,
  total_size INTEGER
);
CREATE TABLE IF NOT EXISTS file_entry (
  id INTEGER PRIMARY KEY,
  archive_id INTEGER NOT NULL REFERENCES archive(id),
  path TEXT NOT NULL,
  name TEXT NOT NULL,
  entry_type TEXT NOT NULL,
  size INTEGER,
  mtime INTEGER
);
CREATE INDEX IF NOT EXISTS idx_archive_snapshot ON archive(snapshot_id);
CREATE INDEX IF NOT EXISTS idx_file_archive ON file_entry(archive_id);
CREATE INDEX IF NOT EXISTS idx_file_name ON file_entry(name);
CREATE INDEX IF NOT EXISTS idx_file_path ON file_entry(path);
`
	return runSQLiteExec(dbPath, schema)
}

func parseHostKey(hostKey string) (string, string) {
	parts := strings.Split(strings.Trim(hostKey, "/"), "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2], parts[len(parts)-1]
	}
	return "", hostKey
}

func snapshotTimeFromDidxPath(didxPath string) string {
	return filepath.Base(filepath.Dir(didxPath))
}

func archiveNameFromEntries(entries []indexedEntry, fallback string) string {
	for _, e := range entries {
		if e.EntryType == "d" && strings.Count(e.Path, "/") == 1 {
			return e.Name
		}
	}
	return fallback
}

func buildIndexSQL(hostKey, didxPath string, df *didxFile, entries []indexedEntry) string {
	backupType, backupID := parseHostKey(hostKey)
	snapshotTime := snapshotTimeFromDidxPath(didxPath)
	didxUUID := formatUUID(df.UUID)
	archiveName := archiveNameFromEntries(entries, filepath.Base(didxPath))
	ctime := time.Unix(int64(df.CTime), 0).UTC().Format(time.RFC3339)

	var b strings.Builder
	b.WriteString("BEGIN;\n")
	fmt.Fprintf(&b, "INSERT INTO host(host_key) VALUES(%s) ON CONFLICT(host_key) DO NOTHING;\n", sqlQuote(hostKey))
	fmt.Fprintf(&b, "INSERT INTO snapshot(host_id, snapshot_time, backup_type, backup_id)\n")
	fmt.Fprintf(&b, "SELECT id, %s, %s, %s FROM host WHERE host_key=%s\n",
		sqlQuote(snapshotTime), sqlQuote(backupType), sqlQuote(backupID), sqlQuote(hostKey))
	fmt.Fprintf(&b, "ON CONFLICT(host_id, snapshot_time) DO UPDATE SET backup_type=excluded.backup_type, backup_id=excluded.backup_id;\n")
	fmt.Fprintf(&b, "INSERT INTO archive(snapshot_id, didx_uuid, archive_name, catalog_didx_path, ctime, chunk_count, total_size)\n")
	fmt.Fprintf(&b, "SELECT s.id, %s, %s, %s, %s, %d, %d\n",
		sqlQuote(didxUUID), sqlQuote(archiveName), sqlQuote(didxPath), sqlQuote(ctime), len(df.Entries), df.TotalSize)
	fmt.Fprintf(&b, "FROM snapshot s JOIN host h ON h.id=s.host_id WHERE h.host_key=%s AND s.snapshot_time=%s\n",
		sqlQuote(hostKey), sqlQuote(snapshotTime))
	b.WriteString("ON CONFLICT(didx_uuid) DO UPDATE SET\n")
	b.WriteString("snapshot_id=excluded.snapshot_id,\n")
	b.WriteString("archive_name=excluded.archive_name,\n")
	b.WriteString("catalog_didx_path=excluded.catalog_didx_path,\n")
	b.WriteString("ctime=excluded.ctime,\n")
	b.WriteString("chunk_count=excluded.chunk_count,\n")
	b.WriteString("total_size=excluded.total_size;\n")
	fmt.Fprintf(&b, "DELETE FROM file_entry WHERE archive_id=(SELECT id FROM archive WHERE didx_uuid=%s);\n", sqlQuote(didxUUID))

	for _, e := range entries {
		sizeVal := "NULL"
		mtimeVal := "NULL"
		if e.Size != nil {
			sizeVal = fmt.Sprintf("%d", *e.Size)
		}
		if e.Mtime != nil {
			mtimeVal = fmt.Sprintf("%d", *e.Mtime)
		}
		fmt.Fprintf(&b,
			"INSERT INTO file_entry(archive_id, path, name, entry_type, size, mtime) VALUES ((SELECT id FROM archive WHERE didx_uuid=%s), %s, %s, %s, %s, %s);\n",
			sqlQuote(didxUUID), sqlQuote(e.Path), sqlQuote(e.Name), sqlQuote(e.EntryType), sizeVal, mtimeVal)
	}

	b.WriteString("COMMIT;\n")
	return b.String()
}

func findDidxFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".pcat1.didx") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func runDecode(args []string) error {
	path := ""
	chunkDir := ""
	scanDir := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--chunk-dir":
			if i+1 >= len(args) {
				return errors.New("missing value for --chunk-dir")
			}
			chunkDir = args[i+1]
			i++
		case "--scan-dir":
			if i+1 >= len(args) {
				return errors.New("missing value for --scan-dir")
			}
			scanDir = args[i+1]
			i++
		case "-h", "--help":
			fmt.Printf("usage: %s [--chunk-dir DIR] <catalog.pcat1|catalog.pcat1.didx>\n", filepath.Base(os.Args[0]))
			fmt.Printf("       %s [--chunk-dir DIR] --scan-dir DIR\n", filepath.Base(os.Args[0]))
			return nil
		default:
			if path != "" {
				return fmt.Errorf("unexpected argument: %s", arg)
			}
			path = arg
		}
	}

	if path == "" {
		path = "catalog.pcat1"
	}

	if scanDir != "" {
		paths, err := findDidxFiles(scanDir)
		if err != nil {
			return fmt.Errorf("scan error: %w", err)
		}
		if len(paths) == 0 {
			return fmt.Errorf("no .pcat1.didx files found in %s", scanDir)
		}
		for _, didxPath := range paths {
			fmt.Printf("=== %s ===\n", didxPath)
			df, err := parseDidx(didxPath)
			if err != nil {
				return fmt.Errorf("didx parse error (%s): %w", didxPath, err)
			}
			printDidxIndex(df)
			catalogData, err := reconstructCatalogFromDidx(didxPath, df, chunkDir)
			if err != nil {
				return fmt.Errorf("reconstruct warning (%s): %w", didxPath, err)
			}
			r := newCatalogReaderFromBytes(catalogData)
			if err := dumpCatalogFromReader(r); err != nil {
				return fmt.Errorf("catalog decode error (%s): %w", didxPath, err)
			}
		}
		return nil
	}

	if strings.HasSuffix(path, ".didx") {
		if chunkDir == "" {
			return fmt.Errorf("didx parse error: --chunk-dir option required: please specify")
		}
		df, err := parseDidx(path)
		if err != nil {
			return fmt.Errorf("didx parse error: %w", err)
		}
		printDidxIndex(df)
		catalogData, err := reconstructCatalogFromDidx(path, df, chunkDir)
		if err != nil {
			return fmt.Errorf("reconstruct warning: %w", err)
		}
		r := newCatalogReaderFromBytes(catalogData)
		return dumpCatalogFromReader(r)
	}

	r, err := newCatalogReader(path)
	if err != nil {
		return fmt.Errorf("open error: %w", err)
	}
	defer r.close()
	return dumpCatalogFromReader(r)
}

func runIndex(args []string) error {
	dbPath := ""
	hostKey := ""
	scanDir := ""
	chunkDir := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db":
			if i+1 >= len(args) {
				return errors.New("missing value for --db")
			}
			dbPath = args[i+1]
			i++
		case "--host":
			if i+1 >= len(args) {
				return errors.New("missing value for --host")
			}
			hostKey = args[i+1]
			i++
		case "--scan-dir":
			if i+1 >= len(args) {
				return errors.New("missing value for --scan-dir")
			}
			scanDir = args[i+1]
			i++
		case "--chunk-dir":
			if i+1 >= len(args) {
				return errors.New("missing value for --chunk-dir")
			}
			chunkDir = args[i+1]
			i++
		case "-h", "--help":
			fmt.Printf("usage: %s index --db DB --scan-dir DIR --chunk-dir DIR [--host HOST_KEY]\n", filepath.Base(os.Args[0]))
			return nil
		default:
			return fmt.Errorf("unexpected argument: %s", args[i])
		}
	}

	if dbPath == "" || scanDir == "" || chunkDir == "" {
		return errors.New("required: --db, --scan-dir, --chunk-dir")
	}
	if hostKey == "" {
		hostKey = filepath.Clean(scanDir)
	}

	if err := ensureSchema(dbPath); err != nil {
		return fmt.Errorf("schema error: %w", err)
	}

	didxFiles, err := findDidxFiles(scanDir)
	if err != nil {
		return fmt.Errorf("scan error: %w", err)
	}
	if len(didxFiles) == 0 {
		return fmt.Errorf("no .pcat1.didx files found in %s", scanDir)
	}

	for _, didxPath := range didxFiles {
		df, err := parseDidx(didxPath)
		if err != nil {
			return fmt.Errorf("didx parse error (%s): %w", didxPath, err)
		}
		catalogData, err := reconstructCatalogFromDidx(didxPath, df, chunkDir)
		if err != nil {
			return fmt.Errorf("reconstruct error (%s): %w", didxPath, err)
		}
		entries, err := collectCatalogEntries(catalogData)
		if err != nil {
			return fmt.Errorf("catalog parse error (%s): %w", didxPath, err)
		}
		sql := buildIndexSQL(hostKey, didxPath, df, entries)
		if err := runSQLiteExec(dbPath, sql); err != nil {
			return fmt.Errorf("sqlite index error (%s): %w", didxPath, err)
		}
		fmt.Printf("indexed %s entries=%d uuid=%s\n", didxPath, len(entries), formatUUID(df.UUID))
	}

	return nil
}

func runSearch(args []string) error {
	dbPath := ""
	hostKey := ""
	pattern := ""
	latestOnly := true

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db":
			if i+1 >= len(args) {
				return errors.New("missing value for --db")
			}
			dbPath = args[i+1]
			i++
		case "--host":
			if i+1 >= len(args) {
				return errors.New("missing value for --host")
			}
			hostKey = args[i+1]
			i++
		case "--file":
			if i+1 >= len(args) {
				return errors.New("missing value for --file")
			}
			pattern = args[i+1]
			i++
		case "--all":
			latestOnly = false
		case "-h", "--help":
			fmt.Printf("usage: %s search --db DB --file PATTERN [--host HOST_KEY] [--all]\n", filepath.Base(os.Args[0]))
			fmt.Printf("wildcards use SQLite GLOB syntax: *, ?, []\n")
			return nil
		default:
			return fmt.Errorf("unexpected argument: %s", args[i])
		}
	}

	if dbPath == "" || pattern == "" {
		return errors.New("required: --db, --file")
	}

	hasWildcard := strings.ContainsAny(pattern, "*?[")
	whereExpr := ""
	if hasWildcard {
		whereExpr = "fe.name GLOB " + sqlQuote(pattern)
	} else {
		whereExpr = "fe.name = " + sqlQuote(pattern)
	}

	limitClause := ""
	if latestOnly {
		limitClause = " LIMIT 1"
	}

	hostFilter := ""
	if hostKey != "" {
		hostFilter = " AND h.host_key = " + sqlQuote(hostKey)
	}

	sql := `
SELECT s.snapshot_time, h.host_key, a.didx_uuid, a.archive_name, fe.path, fe.entry_type,
       COALESCE(CAST(fe.size AS TEXT), ''), COALESCE(CAST(fe.mtime AS TEXT), '')
FROM file_entry fe
JOIN archive a ON a.id = fe.archive_id
JOIN snapshot s ON s.id = a.snapshot_id
JOIN host h ON h.id = s.host_id
WHERE ` + whereExpr + hostFilter + `
ORDER BY s.snapshot_time DESC` + limitClause + `;`

	out, err := runSQLiteQuery(dbPath, sql)
	if err != nil {
		return fmt.Errorf("search query error: %w", err)
	}
	out = strings.TrimRight(out, "\r\n")
	if out == "" {
		fmt.Println("no matches")
		return nil
	}

	lines := strings.Split(out, "\n")
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) < 8 {
			fmt.Println(line)
			continue
		}
		fmt.Printf("snapshot=%s host=%s uuid=%s archive=%s type=%s path=%s", fields[0], fields[1], fields[2], fields[3], fields[5], fields[4])
		if fields[6] != "" {
			fmt.Printf(" size=%s", fields[6])
		}
		if fields[7] != "" {
			if sec, err := strconv.ParseInt(fields[7], 10, 64); err == nil {
				fmt.Printf(" mtime=%s", time.Unix(sec, 0).UTC().Format(time.RFC3339))
			} else {
				fmt.Printf(" mtime=%s", fields[7])
			}
		}
		fmt.Println()
	}

	return nil
}

func main() {
	args := os.Args[1:]
	var err error

	if len(args) > 0 {
		switch args[0] {
		case "index":
			err = runIndex(args[1:])
		case "search":
			err = runSearch(args[1:])
		default:
			err = runDecode(args)
		}
	} else {
		err = runDecode(nil)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
