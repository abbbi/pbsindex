<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [About](#about)
- [What it supports](#what-it-supports)
- [Requirements](#requirements)
- [Usage](#usage)
  - [Indexing and decoding](#indexing-and-decoding)
    - [1. Decode a plain catalog file](#1-decode-a-plain-catalog-file)
    - [2. Decode a specific `.didx`](#2-decode-a-specific-didx)
    - [3. Scan a directory for all `*.pcat1.didx` and decode each](#3-scan-a-directory-for-all-pcat1didx-and-decode-each)
    - [Creating a SQLite database](#creating-a-sqlite-database)
  - [Searching](#searching)
    - [Latest match only (default)](#latest-match-only-default)
    - [Show all matches across all snapshots](#show-all-matches-across-all-snapshots)
    - [Show all matches across all snapshots for specific host](#show-all-matches-across-all-snapshots-for-specific-host)
  - [CLI help](#cli-help)
- [Limitations](#limitations)
- [Links](#links)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# About

`pbsindex` decodes Proxmox `catalog.pcat1` files and dynamic index files
(`.pcat1.didx`), and can index catalog contents into SQLite for fast search.

Using this tool, you can create a searchable file index for all of your
existing file backups within a proxmox backup server datastore.

# What it supports

- Decode plain `catalog.pcat1` and print entries.
- Decode `catalog.pcat1.didx` by resolving chunk blobs from a chunk directory.
- Recursively scan a directory for all `*.pcat1.didx` files.
- Index decoded file lists into SQLite.
- Search indexed files by host, with wildcard support.

# Requirements

Following executables must be existent alongside pbsindex:

- `sqlite3` CLI (for `index` and `search`)
- `zstd` CLI (for compressed PBS chunk blobs)

```bash
sudo apt install sqlite3 zstd
```

# Usage

## Indexing and decoding

### 1. Decode a plain catalog file

```bash
pbsindex /path/to/catalog.pcat1
```

### 2. Decode a specific `.didx`

```bash
pbsindex \
  --chunk-dir /backup/.chunks/ \
  /backup/host/vm178/2026-03-02T10:47:57Z/catalog.pcat1.didx
  d ./backup.pxar.didx
  d ./backup.pxar.didx/bin
  l ./backup.pxar.didx/bin/Mail
  f ./backup.pxar.didx/bin/[ size=55720 mtime=2025-06-04T15:14:05Z
  f ./backup.pxar.didx/bin/aa-enabled size=18672 mtime=2025-04-10T15:06:25Z
  f ./backup.pxar.didx/bin/aa-exec size=18672 mtime=2025-04-10T15:06:25Z
  f ./backup.pxar.didx/bin/aa-features-abi size=18664 mtime=2025-04-10T15:06:25Z
  l ./backup.pxar.didx/bin/apropos
  f ./backup.pxar.didx/bin/apt size=18752 mtime=2025-06-24T17:02:46Z
  [..]
```

### 3. Scan a directory for all `*.pcat1.didx` and decode each

```bash
pbsindex \
  --chunk-dir /backup/.chunks/ \
  --scan-dir /backup/host/vm178/
```

### Creating a SQLite database

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
  --file 'iptables'
  snapshot=2026-03-02T10:47:57Z host=/backup/host/vm178 uuid=7e4086a9-4432-4184-a21f-0aeec2b2de93 archive=JO.pxar.didx type=l path=/JO.pxar.didx/sbin/iptables
```

### Show all matches across all snapshots

```bash
pbsindex search \
  --db /tmp/pcat.db \
  --file 'iptables' \
  --all
 snapshot=2026-03-02T10:47:57Z host=/backup/host/vm178 uuid=7e4086a9-4432-4184-a21f-0aeec2b2de93 archive=JO.pxar.didx type=l path=/JO.pxar.didx/sbin/iptables
 snapshot=2026-03-02T10:47:57Z host=/backup/host/vm178 uuid=7e4086a9-4432-4184-a21f-0aeec2b2de93 archive=JO.pxar.didx type=f path=/JO.pxar.didx/share/bash-completion/completions/iptables size=2109 mtime=2025-01-26T19:49:00Z
 snapshot=2026-03-02T10:47:57Z host=/backup/host/vm178 uuid=7e4086a9-4432-4184-a21f-0aeec2b2de93 archive=JO.pxar.didx type=d path=/JO.pxar.didx/share/doc/iptables
 snapshot=2026-03-02T10:47:57Z host=/backup/host/vm178 uuid=7e4086a9-4432-4184-a21f-0aeec2b2de93 archive=JO.pxar.didx type=d path=/JO.pxar.didx/share/iptables
```

### Show all matches across all snapshots for specific host

```bash
pbsindex search \
  --db /tmp/pcat.db \
  --host backup/host/vm178 \
  --file 'iptables' \
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

# Limitations

Encrypted blobs cannot be indexed.

# Links

 https://pbs.proxmox.com/docs/file-formats.html
