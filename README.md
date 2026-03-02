<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
- [pbsindex](#pbsindex)
  - [What it supports](#what-it-supports)
  - [Requirements](#requirements)
  - [Usage](#usage)
    - [1. Decode a plain catalog file](#1-decode-a-plain-catalog-file)
    - [2. Decode a specific `.didx`](#2-decode-a-specific-didx)
    - [3. Scan a directory for all `*.pcat1.didx` and decode each](#3-scan-a-directory-for-all-pcat1didx-and-decode-each)
  - [SQLite indexing](#sqlite-indexing)
  - [Searching](#searching)
    - [Latest match only (default)](#latest-match-only-default)
    - [Show all matches across all snapshots](#show-all-matches-across-all-snapshots)
    - [Show all matches across all snapshots for specific host](#show-all-matches-across-all-snapshots-for-specific-host)
  - [CLI help](#cli-help)
  - [Limitations](#limitations)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# pbsindex

`pbsindex` decodes Proxmox `catalog.pcat1` files and dynamic index files
(`.pcat1.didx`), and can index catalog contents into SQLite for fast search.

## What it supports

- Decode plain `catalog.pcat1` and print entries.
- Decode `catalog.pcat1.didx` by resolving chunk blobs from a chunk directory.
- Recursively scan a directory for all `*.pcat1.didx` files.
- Index decoded file lists into SQLite.
- Search indexed files by host, with wildcard support.

## Requirements

Following executables must be existent alongside pbsindex:

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
  --chunk-dir /backup/.chunks/ \
  /backup/host/vm178/2026-03-02T10:47:57Z/catalog.pcat1.didx
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
  --scan-dir /backup/host/vm178 \
  --chunk-dir /backup/.chunks/
indexed /backup/host/vm178/2026-03-02T10:36:15Z/catalog.pcat1.didx entries=1415 uuid=09d276f0-96a0-4e7e-8208-d4b54fcb6ccd
indexed /backup/host/vm178/2026-03-02T10:47:57Z/catalog.pcat1.didx entries=48820 uuid=7e4086a9-4432-4184-a21f-0aeec2b2de93
```

Notes:

- `archive.didx_uuid` is unique and used as the stable identity for a catalog file list.
- Re-indexing is idempotent for the same UUID (archive metadata and file entries are updated).
- `--host` is optional. If omitted, host key defaults to `--scan-dir` (normalized path).
  If you want a shorter stable key, pass `--host` explicitly.

## Searching

Use `search` to query indexed files for a host.

### Latest match only (default)

```bash
pbsindex search \
  --db /tmp/pcat.db \
  --file '*iptables-save*'
```

### Show all matches across all snapshots

```bash
pbsindex search \
  --db /tmp/pcat.db \
  --file 'iptables-save' \
  --all
```

### Show all matches across all snapshots for specific host

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

## Limitations

Encrypted blobs cannot be indexed.

