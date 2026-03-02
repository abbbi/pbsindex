# catalog_dump

`pbsindex` decodes Proxmox `catalog.pcat1` files and dynamic index files
(`.pcat1.didx`), and can index catalog contents into SQLite for fast search.

## What it supports

- Decode plain `catalog.pcat1` and print entries.
- Decode `catalog.pcat1.didx` by resolving chunk blobs from a chunk directory.
- Recursively scan a directory for all `*.pcat1.didx` files.
- Index decoded file lists into SQLite.
- Search indexed files by host, with wildcard support.

## Requirements

- Go toolchain (`go run ...`)
- `sqlite3` CLI (for `index` and `search`)
- `zstd` CLI (for compressed PBS chunk blobs)

## Usage

### 1. Decode a plain catalog file

```bash
pbsindex /path/to/catalog.pcat1
```

### 2. Decode a specific `.didx`

```bash
pbsindex \
  --chunk-dir /tmp/backup/.chunks/ \
  /tmp/backup/host/vm178/2026-03-02T10:47:57Z/catalog.pcat1.didx
```

### 3. Scan a directory for all `*.pcat1.didx` and decode each

```bash
pbsindex \
  --chunk-dir /tmp/backup/.chunks/ \
  --scan-dir /tmp/backup/host/vm178/
```

## SQLite indexing

Use `index` to ingest all `.pcat1.didx` files under a host path into SQLite.

```bash
pbsindex index \
  --db /tmp/pcat.db \
  --scan-dir /tmp/backup/host/vm178 \
  --chunk-dir /tmp/backup/.chunks/
```

Notes:

- `archive.didx_uuid` is unique and used as the stable identity for a catalog file list.
- Re-indexing is idempotent for the same UUID (archive metadata and file entries are updated).
- `--host` is optional. If omitted, host key defaults to `--scan-dir` (normalized path).
  If you want a shorter stable key, pass `--host` explicitly.

## Search

Use `search` to query indexed files for a host.

### Latest match only (default)

```bash
pbsindex search \
  --db /tmp/pcat.db \
  --file '*iptables-save*'
```

### Show all matches across snapshots

```bash
pbsindex search \
  --db /tmp/pcat.db \
  --host backup/host/vm178 \
  --file 'iptables-save' \
  --all
```

Wildcard behavior:

- If pattern contains `*`, `?`, or `[]`, SQLite `GLOB` is used.
- Otherwise exact filename match is used (`name = pattern`).
- `--host` is optional for search. If omitted, search runs across all indexed hosts.

## CLI help

```bash
pbsindex --help
pbsindex index --help
pbsindex search --help
```
