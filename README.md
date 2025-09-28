# wipefile

A secure file deletion tool that overwrites files with realistic fake headers before deletion, making forensic data recovery significantly more difficult.

## Key Security Feature

Instead of overwriting files with simple patterns or random data, wipefile uses **fake file headers** that mimic real file formats:

- JPEG/PNG image headers
- PDF document headers
- ZIP/RAR archive headers
- Executable file headers (ELF, Mach-O)
- Encrypted wallet patterns
- System log entries
- Multiple different realistic patterns

This approach creates **forensic confusion** - recovered data fragments appear to be legitimate files rather than obvious overwrite patterns, making it much harder for forensic tools to distinguish between real recovered files and fake overwrite data.

## Usage

```bash
# Basic file wiping
./wipefile document.txt

# Recursive directory wiping
./wipefile -r directory/

# Wipe free disk space
./wipefile -s

# Parallel processing (up to 5 workers)
./wipefile -p 3 *.txt

# Verbose output
./wipefile -v file.txt
```

## How It Works

1. **Overwrite** file with realistic fake headers (every 4KB, a new header, rest random data)
2. **Truncate** file to zero bytes
3. **Rename** to random string
4. **Delete** from filesystem

This should prevent any forensic undelete or data recovery, and will hopefully make the process much more time consuming, than just overwriting with random data.

## Download

Pre-compiled binaries available: [Download wipefile here](https://github.com/andersdotio/wipefile/releases/tag/v1.0)

## Build

```bash
go build -o wipefile main.go
```

## Options

- `-v` - Verbose output
- `-r` - Recursive directories
- `-p N` - N parallel workers (1-5)
- `-s` - Wipe free space
- `-t` - Test mode (show sample pattern)
