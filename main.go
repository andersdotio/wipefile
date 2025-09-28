package main

import (
	"bytes"
	cryptoRand "crypto/rand"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	version = "1.0"
	bufferSize = 4096
	maxParallelWorkers = 5
	freeSpaceChunkSize = 3 * 1024 * 1024 * 1024 // 3GB
)

var (
	showVersion = flag.Bool("version", false, "Show version information")
	verbose     = flag.Bool("v", false, "Verbose output")
	parallel    = flag.Int("p", 1, "Process X files in parallel (1-5)")
	recursive   = flag.Bool("r", false, "Recursive processing of directories")
	freeSpace   = flag.Bool("s", false, "Fill free disk space with random files in current directory")
	testMode    = flag.Bool("t", false, "Test mode - generate and display sample fake header")
)

func main() {
	flag.Parse()

	if *showVersion {
		fmt.Printf("wipefile v%s - by Anders Nilsson - https://github.com/andersdotio/wipefile\n", version)
		return
	}

	if *testMode {
		header := getFakeHeader()
		os.Stdout.Write(header)
		return
	}

	if *parallel < 1 || *parallel > maxParallelWorkers {
		fmt.Fprintf(os.Stderr, "Error: parallel workers must be between 1 and %d\n", maxParallelWorkers)
		os.Exit(1)
	}

	rand.Seed(time.Now().UnixNano())

	if *freeSpace {
		wipeFreeSpace()
		return
	}

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <file1> [file2] ...\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(1)
	}

	// WaitGroups coordinate completion of all workers before proceeding
	var fileWg sync.WaitGroup
	var folderWg sync.WaitGroup
	fileChan := make(chan string, 100)  // Queue up to 100 files without blocking main thread
	folderChan := make(chan string, 50) // Queue up to 50 folders without blocking main thread

	// Start file workers
	for i := 0; i < *parallel; i++ {
		fileWg.Add(1)
		go func() {
			defer fileWg.Done()
			for file := range fileChan {
				wipeFile(file)
			}
		}()
	}

	// Folder worker (single threaded, less issues)
	folderWg.Add(1)
	go func() {
		defer folderWg.Done()
		for folder := range folderChan {
			wipeFolder(folder)
		}
	}()

	var files []string
	var folders []string

	for _, arg := range args {
		collectPaths(arg, &files, &folders)
	}

	// Sort folders depth-first (deepest paths first) to avoid trying to delete parent before child
	sort.Slice(folders, func(i, j int) bool {
		depthI := strings.Count(folders[i], string(os.PathSeparator))
		depthJ := strings.Count(folders[j], string(os.PathSeparator))
		return depthI > depthJ
	})

	// Process files first before all folders (parallel safe)
	for _, file := range files {
		fileChan <- file
	}
	close(fileChan) // Signal no more files coming

	fileWg.Wait()

	// Process folders after all files are deleted
	for _, folder := range folders {
		folderChan <- folder
	}
	close(folderChan)

	folderWg.Wait()

}

func collectPaths(path string, files *[]string, folders *[]string) {
	info, err := os.Lstat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wipefile: cannot wipe '%s': %s\n", path, getSimpleError(err))
		return
	}

	if info.IsDir() {
		if *recursive {
			*folders = append(*folders, path)
			entries, err := os.ReadDir(path)
			if err != nil {
				if *verbose {
					fmt.Fprintf(os.Stderr, "wipefile: cannot read directory '%s': %s\n", path, getSimpleError(err))
				}
				return
			}
			for _, entry := range entries {
				fullPath := filepath.Join(path, entry.Name())
				collectPaths(fullPath, files, folders)
			}
		} else {
			fmt.Fprintf(os.Stderr, "wipefile: cannot wipe '%s': Is a directory\n", path)
		}
	} else {
		*files = append(*files, path)
	}
}

func wipeFile(filePath string) {
	if *verbose {
		fmt.Printf("wiping file: %s\n", filePath)
	}

	info, err := os.Lstat(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wipefile: cannot wipe '%s': %s\n", filePath, getSimpleError(err))
		return
	}

	if !isSpecialFile(info) {
		if !overwriteAndTruncate(filePath) {
			return
		}
	} else if *verbose {
		fmt.Printf("special file (no overwrite): '%s'\n", filePath)
	}

	newPath := renameToRandomName(filePath)
	if newPath == "" {
		return
	}

	if err := os.Remove(newPath); err != nil {
		if *verbose {
			fmt.Fprintf(os.Stderr, "wipefile: cannot remove '%s': %s\n", newPath, getSimpleError(err))
		}
	} else if *verbose {
		fmt.Printf("removed '%s'\n", newPath)
	}
}

func overwriteAndTruncate(filePath string) bool {
	info, err := os.Stat(filePath)
	if err != nil {
		if *verbose {
			fmt.Fprintf(os.Stderr, "wipefile: cannot get info for '%s': %s\n", filePath, getSimpleError(err))
		}
		return false
	}
	originalSize := info.Size()

	file, err := os.OpenFile(filePath, os.O_WRONLY, 0)
	if err != nil {
		if *verbose {
			fmt.Fprintf(os.Stderr, "wipefile: cannot open '%s': %s\n", filePath, getSimpleError(err))
		}
		return false
	}

	// Overwrite all of the file with fake header-buffers
	bytesWritten := int64(0)
	for bytesWritten < originalSize {
		buffer := getFakeHeader()
		if _, err := file.Write(buffer); err != nil {
			file.Close()
			if *verbose {
				fmt.Fprintf(os.Stderr, "wipefile: cannot write to '%s': %s\n", filePath, getSimpleError(err))
			}
			return false
		}
		bytesWritten += int64(len(buffer))
	}

	// Sync to tell storage to actually write any cached data
	if err := file.Sync(); err != nil {
		file.Close()
		if *verbose {
			fmt.Fprintf(os.Stderr, "wipefile: cannot sync '%s': %s\n", filePath, getSimpleError(err))
		}
		return false
	}

	file.Close()

	return truncateFile(filePath)
}

func truncateFile(filePath string) bool {
	file, err := os.OpenFile(filePath, os.O_WRONLY, 0)
	if err != nil {
		if *verbose {
			fmt.Fprintf(os.Stderr, "wipefile: cannot reopen for truncate '%s': %s\n", filePath, getSimpleError(err))
		}
		return false
	}
	defer file.Close()

	if err := file.Truncate(0); err != nil {
		if *verbose {
			fmt.Fprintf(os.Stderr, "wipefile: cannot truncate '%s': %s\n", filePath, getSimpleError(err))
		}
		return false
	}

	return true
}

func renameToRandomName(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	randomName := make([]byte, len(base))
	for i := range randomName {
		randomName[i] = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"[rand.Intn(62)]
	}

	newPath := filepath.Join(dir, string(randomName))
	if err := os.Rename(path, newPath); err != nil {
		if *verbose {
			fmt.Fprintf(os.Stderr, "wipefile: cannot rename '%s': %s\n", path, getSimpleError(err))
		}
		return path // Return original path so deletion still happens
	}

	if *verbose {
		fmt.Printf("renamed '%s' -> '%s'\n", path, newPath)
	}
	return newPath
}

func wipeFolder(folderPath string) {
	if *verbose {
		fmt.Printf("wiping folder: %s\n", folderPath)
	}

	newPath := renameToRandomName(folderPath)
	if newPath == "" {
		return
	}

	if err := os.Remove(newPath); err != nil {
		if *verbose {
			fmt.Fprintf(os.Stderr, "wipefile: cannot remove directory '%s': %s\n", newPath, getSimpleError(err))
		}
	} else if *verbose {
		fmt.Printf("removed directory '%s'\n", newPath)
	}
}

func isSpecialFile(info os.FileInfo) bool {
	mode := info.Mode()
	return mode&os.ModeSymlink != 0 ||
		mode&os.ModeNamedPipe != 0 ||
		mode&os.ModeSocket != 0 ||
		mode&os.ModeDevice != 0 ||
		mode&os.ModeCharDevice != 0
}

func wipeFreeSpace() {
	fmt.Printf("wiping free space in current directory...\n")

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wipefile: cannot get current directory: %s\n", getSimpleError(err))
		return
	}

	tempDir := filepath.Join(cwd, fmt.Sprintf("wipefile_temp_%d", time.Now().Unix()))
	if err := os.Mkdir(tempDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "wipefile: cannot create temp directory: %s\n", getSimpleError(err))
		return
	}
	defer os.RemoveAll(tempDir)

	counter := 0
	for {
		filename := filepath.Join(tempDir, fmt.Sprintf("wipe_%d.tmp", counter))
		file, err := os.Create(filename)
		if err != nil {
			if *verbose {
				fmt.Fprintf(os.Stderr, "wipefile: cannot create temp file: %s\n", getSimpleError(err))
			}
			break
		}

		written := int64(0)
		diskFull := false
		for written < freeSpaceChunkSize {
			buffer := getFakeHeader()
			n, err := file.Write(buffer)
			if err != nil {
				file.Close()
				if *verbose {
					fmt.Printf("disk full, stopping freespace wipe\n")
				}
				diskFull = true
				break
			}
			written += int64(n)
		}

		file.Close()
		counter++

		if *verbose {
			fmt.Printf("created temp file %d (%d MB)\n", counter, written/(1024*1024))
		}

		if diskFull {
			break
		}
	}

	if *verbose {
		fmt.Printf("cleaning up temporary files...\n")
	}

	entries, err := os.ReadDir(tempDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				tempFile := filepath.Join(tempDir, entry.Name())

				truncateFile(tempFile)

				newPath := renameToRandomName(tempFile)
				if newPath != "" {
					if err := os.Remove(newPath); err != nil {
						if *verbose {
							fmt.Fprintf(os.Stderr, "wipefile: cannot remove '%s': %s\n", newPath, getSimpleError(err))
						}
					} else if *verbose {
						fmt.Printf("removed '%s'\n", newPath)
					}
				}
			}
		}
	}

	finalTempDir := renameToRandomName(tempDir)
	if finalTempDir != "" {
		if err := os.Remove(finalTempDir); err != nil {
			if *verbose {
				fmt.Fprintf(os.Stderr, "wipefile: cannot remove directory '%s': %s\n", finalTempDir, getSimpleError(err))
			}
		} else if *verbose {
			fmt.Printf("removed directory '%s'\n", finalTempDir)
		}
	}

	if *verbose {
		fmt.Printf("free space wipe completed\n")
	}
}

func getFakeHeader() []byte {
	patterns := []string{
		// .7z
		"7z\\bc\\af\\27\\1c\\00\\04",

		// .avi
		"RIFF%x%x%x%xAVI LIST&\\01\\00\\00\\hdrlavih8\\00\\00\\00%x%x%x\\00\\00\\00\\00\\00\\00\\00\\00\\00\\10\\01\\00\\00%x\\00\\00\\00\\00\\00\\00\\00\\02\\00\\00\\00\\00\\00\\00\\00\\00\\05\\00\\00\\d0\\02\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00LISTt\\00\\00\\00strlstrh8\\00\\00\\00vidsH264\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00@B\\0f\\00%x%x\\0f\\00\\00\\00\\00\\00",
		"RIFF%x%x%x%xAVI LIST\\7e\\22\\00\\00hdrlavih8\\00\\00\\00%x%x%x\\00\\00\\00\\00\\00\\00\\00\\00\\00\\10\\01\\00\\00%x%x%x00\\00\\00\\00\\00\\02\\00\\00\\00\\00\\00\\00\\00\\70\\02\\00\\00\\00\\01\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\LIST\\94\\10\\00\\00strlstrh8\\00\\00\\00vidsxvid\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00",
		"RIFF%x%x%x%xAVI LIST\\54\\01\\00\\00hdrlavih8\\00\\00\\00\\35\\82\\00\\00\\20\\a1\\07\\00\\00\\00\\00\\00\\10\\00\\01\\00\\83\\04\\00\\00\\00\\00\\00\\00\\02\\00\\00\\00\\00\\ee\\02\\00\\80\\02\\00\\00\\e0\\01\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00LIST\\a2\\00\\00\\00strlstrh8\\00\\00\\00vidsmjpg\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\35\\82\\00\\00\\40\\42\\0f\\00\\00\\00\\00\\00",

		// .bat
		"@echo off\\0a\\0a?%t%t%t%t%t?\\0a\\0a?%t%t%t%t%t?",

		// Berkeley DB (Btree, version 9, native byte-order)
		"\\00\\00\\00\\00\\01\\00\\00\\00\\00\\00\\00\\00b1\\05\\00\\09\\00\\00\\00\\00\\10\\00\\00\\00\\09\\00\\00%x\\00\\00\\00\\14\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\20\\00\\00\\00",

		// bitlocker
		"\\eb\\58\\90-FVE-FS-\\02\\00\\00\\08\\00\\00\\00\\00\\00%x%x%x\\00\\00\\3f\\00%x%x%x%x%x%x\\00\\00\\00\\00\\e0\\1f\\00\\00\\00\\00\\00\\00%x%x%x%x\\01\\00\\06\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\80\\00\\29%x%x%x%x%b%b%b%b%b%b%b%b%b%b%bFAT32   ",

		// Blockchain wallet backup
		"{\"pbkdf2_iterations\":5000,\"version\":2,\"payload\":\"%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b",
		"{\\0a        \"guid\" : \"%h%h%h%h%h%h%h%h-%h%h%h%h-%h%h%h%h-%h%h%h%h-%h%h%h%h%h%h%h%h%h%h%h%h\",\\0a        \"sharedKey\" : \"%h%h%h%h%h%h%h%h-%h%h%h%h-%h%h%h%h-%h%h%h%h-%h%h%h%h%h%h%h%h%h%h%h%h\",\\0a        \"options\" : {\"pbkdf2_iterations\":10,\"fee_policy\":0,\"",

		// bootsector
		"\\eb\\63\\90\\10\\8e\\d0\\bc\\00\\b0\\b8\\00\\00\\8e\\d8\\8e\\c0\\fb\\be",

		// .c
		"int main(void) { %t%t%t%t%t?",

		// .deb
		"\\21\\3c\\61\\72\\63\\68\\3e\\0a\\64\\65\\62\\69\\61\\6e\\2d\\62\\69\\6e\\61\\72\\79\\2f\\20\\20\\30\\20\\20\\20\\20\\20\\20\\20\\20\\20\\20\\20\\30\\20\\20\\20\\20\\20\\30\\20\\20\\20\\20\\20\\36\\34\\34\\20\\20\\20\\20\\20\\34\\20\\20\\20\\20\\20\\20\\20\\20\\20\\60\\0a\\32\\2e\\30\\0a",

		// Dockerfile
		"FROM python:3.10-alpine\\0a\\0a?EXPOSE %d%d%d%d?\\0a\\0a?COPY \"%l%l%l%l%l?",
		"FROM node:latest\\0a\\0a?RUN wget -q -O - https://%l%l%l%l%l?",

		// Electrum wallet
		"{\\0a    \"accounts_expanded\": {},\\0a    \"addr_history\": {\\0a        \"1%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b\": [],\\0a        \"1%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b\": [],\\0a",
		"{\\0a    \"accounts_expanded\": {},\\0a    \"addr_history\": {\\0a        \"1%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b\": [],\\0a        \"1%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b\": [],\\0a        \"1%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b\": [],\\0a",
		"{\\0a    \"accounts\": {\\0a        \"0\": {\\0a            \"change\": [\\0a                \"0%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h\",\\0a                \"0%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h%h\",\\0a",
		"{\\0a    \"addr_history\": {\\0a        \"1%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b\": [],\\0a        \"1%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b\": [],\\0a",
		"{\\0a    \"addr_history\": {\\0a        \"1%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b\": [],\\0a        \"1%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b\": [],\\0a        \"1%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b\": [],\\0a",

		// .elf
		"\\7fELF\\02\\01\\01\\00\\00\\00\\00\\00\\00\\00\\00\\00\\03\\00\\3e\\00\\01\\00\\00\\00",

		// .gz
		"\\1f\\8b\\08\\08%x%x%x%x\\00\\03",

		// Go
		"module %c%c%c%c%c",
		"package main\\0a\\0a",

		// hosts
		"127.0.0.1\\09localhost\\0a\\0a?%d%d.%d%d.%d%d.%d%d\\09%l%l%l%l%l%l",

		// Java keystore
		"\\fe\\ed\\fe\\ed\\00\\02",

		// .jpg
		"\\ff\\d8\\ff\\e0\\00\\10\\4a\\46\\49\\46\\00\\01\\01\\01\\00%x\\00%x\\00\\00\\ff\\e1\\00\\68\\45\\78\\69\\66\\00\\00",
		"\\ff\\d8\\ff\\e0\\00\\10\\4a\\46\\49\\46\\00\\01\\01\\00\\00\\01\\00\\01\\00\\00\\ff\\fe\\00\\3b%b%b%b%b",

		// .json
		"{\\0a  \"%l%l%l%l%l%l?",

		// LUKS
		"LUKS\\ba\\be\\00\\02\\00\\00\\00\\00\\00\\00\\40\\00\\00\\00\\00\\00\\00\\00\\00\\03\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00sha256\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00",

		// Mach-O 64-bit x86_64 executable
		"\\cf\\fa\\ed\\fe\\07\\00\\00\\01\\03\\00\\00\\00\\02\\00\\00\\00\\0e\\00\\00\\00%x%x\\00\\00\\04\\00\\20\\00\\00\\00\\00\\00\\19\\00\\00\\00\\48\\00\\00\\00__PAGEZERO\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\01\\00\\00",

		// .mp4
		"\\00\\00\\00 ftypiso5\\00\\00\\00\\01iso5dsmsmsixdash\\00\\00\\00",
		"\\00\\00\\00\\18ftypisom\\00\\00\\00\\00isom3gp4\\02\\fb\\4f\\edmdat\\20\\00\\0c\\41\\f9\\00\\00\\c4\\1f\\90\\e0\\20\\00\\0c\\41\\f9\\00\\00\\c4\\1f\\90\\e0\\20\\00",
		"\\00\\00\\00 ftypisom\\00\\00\\02\\00isomiso2avc1mp4100%x%x%xmoov\\00\\00\\00lmvhd\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\03\\e8\\00",

		// MySQL dump
		"-- MySQL dump 10.1%d  Distrib 8.%d.%d%d, for Linux (x86_64)\\0a",
		"-- MySQL dump 10.1%d  Distrib 1%d.%d.%d%d-MariaDB, for debian-linux-gnu (x86_64)\\0a",
		"CREATE TABLE `%l%l%l%l%l?%l?` (\\0a  `%l%l%l%l%l%l?",
		"INSERT INTO `%l%l%l%l%l?%l?` (`%l%l%l%l%l?",

		// MySQL replication log
		"\\fe\\62\\69\\6e%x%x%x%x\\0f\\01\\00\\00\\00\\7a\\00\\00\\00\\7e\\00\\00\\00\\00\\00\\04\\00",

		// .pdf
		"%PDF-1.7\\0a1 0 obj\\0a<< /Type /Catalog >>\\0aendobj\\0a2 0 obj\\0a<< /Filter /FlateDecode\\0a/Length %d%d%d%d%d? >>\\0astream\\0a",

		// .php
		"<?php\\0a\\0a?%c%c%c%c%c%c%c%c",

		// pgp/ssh
		"---BEGIN PGP PRIVATE KEY BLOCK---\\0a\\0a%b%b%b%b%b%b",
		"-----BEGIN PGP SIGNED MESSAGE-----\\0a\\0a%b%b%b%b%b%b",
		"-----BEGIN OPENSSH PRIVATE KEY-----\\0a\\0a%b%b%b%b%b%b",
		"-----BEGIN RSA PRIVATE KEY-----\\0a\\0a%b%b%b%b%b%b",
		"---- BEGIN SSH2 PUBLIC KEY ----\\0a\\0a%b%b%b%b%b%b",
		"-----BEGIN CERTIFICATE-----\\0a\\0a%b%b%b%b%b%b",
		"ssh-rsa %b%b%b%b%b%b",
		"ssh-ed25519 %b%b%b%b%b%b",

		// .png
		"\\89\\50\\4e\\47\\0d\\0a\\1a\\0a\\00\\00\\00\\0d\\49\\48\\44\\52\\00\\00%x%x\\00\\00%x%x%b",

		// .py
		"#!/usr/bin/env python3\\0a",

		// QEMU QCOW
		"QFI\\fb\\00\\00\\00\\03\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\10\\00\\00\\00\\03",

		// .rar
		"\\52\\61\\72\\21\\1A\\07\\01\\00",

		// sector formats
		"RRaA\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00",
		"\\f0\\ff\\ff\\0f\\ff\\ff\\ff\\0f\\ff\\ff\\ff\\0f",

		// shell script
		"#!/bin/sh\\0a\\0a?",
		"#!/bin/bash\\0a\\0a?",
		"#!/bin/bash\\0a\\0a?%t%t%t%t%t%t%t%t?%t?%t?\\0a\\0a?%t%t%t%t%t%t%t?%t?",

		// VDI
		"<<< Oracle VM VirtualBox Disk Image >>>\\0a\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\00\\7f\\10\\da\\be\\01\\00\\01\\00\\90\\01\\00\\00\\01\\00\\00\\00",

		// VMware VMDK
		"# Disk DescriptorFile\\0aversion=3\\0aencoding=\"UTF-8\"\\0aCID=%h%h%h%h%h%h%h%h\\0aparentCID=ffffffff\\0a",

		// wallet - generic encrypted
		"{\"encrypted\":\"",

		// .xml
		"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\\0a<!DOCTYPE %t%t%t%t%t%t%t?",
		"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\\0a<%t%t%t%t%t%t%t?",

		// .zip
		"\\50\\4b\\03\\04\\14\\00\\08\\00\\08\\00",

		// zlib
		"\\78\\da\\b9\\f7\\00\\00\\00\\00\\00\\01",

		// extra: command and log leftovers
		".bash_history\\0a%t%t%t%t",
		"exit\\0a\\0a?%t%t%t%t",
		"ls -al\\0a%t%t%t%t",
		"sudo su -\\0a%t%t%t%t",
		"cd ..\\0a%t%t%t%t?",
		"sudo rm *\\0a%t%t%t%t",
		"df -h\\0a%t%t%t%t?",
		"whoami\\0a%t%t%t%t",
		"uname -a\\0a%t%t%t%t",
		"$ ls -al\\0atotal 2096\\0adrwxrwxr-x\\092%t%t%t%t",
		"Enter passphrase for key 'id_rsa': %t%t%t%t",
		"sudo: 1 incorrect password attempt",
		"sudo: pam_unix(sudo:auth): conversation failed",
		"sudo: pam_unix(sudo:auth): auth could not identify password",
		"CRON[1%d%d%d%d?]: pam_unix(cron:session): session closed for user %t%t%t%t",
		"CRON[1%d%d%d%d?]: pam_unix(cron:session): session opened for user root(uid=0) by (uid=0)",
		"USER=root ; COMMAND=/usr/bin/vim %t%t%t%t",
		"gpgv: Signature made ",
		"using RSA key %H%H%H%H%H%H%H%H%H%H%H%H%H%H%H%H",
		"Adding user %l%l%l%l? to group adm",
		"Adding user %l%l%l%l? to group %l%l%l%l",
		"kernel: [%d%d%d%d%d%d%d.%d%d%d%d%d%d] usb 1-%d: New USB device found, idVendor=%h%h%h%h, idProduct=01%h%h, bcdDevice= 1.%d%d",

		// extra: encrypted text
		"Encrypted: ",
		"enc: ",
		"data: ",

		// extra: guid
		"%h%h%h%h%h%h%h%h-%h%h%h%h-%h%h%h%h-%h%h%h%h-%h%h%h%h%h%h%h%h%h%h%h%h",
		"{%h%h%h%h%h%h%h%h-%h%h%h%h-%h%h%h%h-%h%h%h%h-%h%h%h%h%h%h%h%h%h%h%h%h}",

		// extra: pw-text
		"Password: %b%b%b%b%b%b%b%b",
		"pw: %b%b%b%b%b%b%b%b",

		// extra: text
		"%b%b%b%b%b%b%b%b%b%b%b",
		"%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b",
		"%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b%b",
		"%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t",
		"%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t%t",

		// all random
		"",
	}

	selectedPattern := patterns[rand.Intn(len(patterns))]
	return generateBuffer(selectedPattern)
}

func generateBuffer(input string) []byte {
	var buf bytes.Buffer
	i := 0
	for i < len(input) {
		if input[i] == '?' && i > 0 {
			// Optional character: 50% chance of including the previous character
			if rand.Intn(2) == 0 {
				// Remove the last character that was just added
				if buf.Len() > 0 {
					bufBytes := buf.Bytes()
					buf.Reset()
					buf.Write(bufBytes[:len(bufBytes)-1])
				}
			}
			i++
			continue
		} else if input[i] == '%' {
			if i+1 < len(input) {
				switch input[i+1] {
				case 'd':
					// Generate random number 0-9
					num := rand.Intn(10)
					buf.WriteByte('0' + byte(num))
					i += 2
				case 'c':
					// Generate random char a-zA-Z
					ch := rand.Intn(52)
					if ch < 26 {
						buf.WriteByte('A' + byte(ch))
					} else {
						buf.WriteByte('a' + byte(ch) - 26)
					}
					i += 2
				case 'x':
					// Generate random char 0-255
					randBytes := make([]byte, 1)
					cryptoRand.Read(randBytes)
					buf.WriteByte(randBytes[0])
					i += 2
				case 'b':
					// Generate random char a-zA-Z0-9
					ch := rand.Intn(62)
					if ch < 10 {
						buf.WriteByte('0' + byte(ch))
					} else if ch < 36 {
						buf.WriteByte('A' + byte(ch) - 10)
					} else {
						buf.WriteByte('a' + byte(ch) - 36)
					}
					i += 2
				case 'l':
					// Generate a letter a-z
					buf.WriteByte(byte('a' + rand.Intn(26))) // a-z
					i += 2
				case 't':
					if rand.Intn(6) < 5 { // Either a letter or a space
						buf.WriteByte(byte('a' + rand.Intn(26))) // a-z
					} else {
						buf.WriteByte(' ') // space
					}
					i += 2
				case 'h':
					// Generate random char for hexadecimal [0-9, a-f]
					ch := rand.Intn(16)
					if ch < 10 {
						buf.WriteByte('0' + byte(ch))
					} else {
						buf.WriteByte('a' + byte(ch) - 10)
					}
					i += 2
				case 'H':
					// Generate random char for hexadecimal [0-9, a-f]
					ch := rand.Intn(16)
					if ch < 10 {
						buf.WriteByte('0' + byte(ch))
					} else {
						buf.WriteByte('A' + byte(ch) - 10)
					}
					i += 2
				default:
					buf.WriteByte('%')
					i += 1
				}
				continue
			}
		} else if i+2 < len(input) && input[i] == '\\' {
			// Convert next 2 characters to byte value
			value, err := strconv.ParseUint(input[i+1:i+3], 16, 8)
			if err == nil {
				buf.WriteByte(byte(value))
				i += 3
				continue
			}
		}
		buf.WriteByte(input[i])
		i++
	}

	// Pad the buffer to 4K with random data
	paddingSize := bufferSize - buf.Len()
	if paddingSize > 0 {
		padding := make([]byte, paddingSize)
		cryptoRand.Read(padding)
		buf.Write(padding)
	}

	return buf.Bytes()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func getSimpleError(err error) string {
	// Go errors are usually not pretty, so let's clean them up
	// instead of:
	// wipefile: cannot wipe 'non-existing.txt': lstat non-existing.txt: no such file or directory
	// let's make it:
	// wipefile: cannot wipe 'non-existing.txt': No such file or directory
	errStr := err.Error()
	if strings.Contains(errStr, "no such file or directory") {
		return "No such file or directory"
	}
	if strings.Contains(errStr, "permission denied") {
		return "Permission denied"
	}
	if strings.Contains(errStr, "is a directory") {
		return "Is a directory"
	}
	if strings.Contains(errStr, "not a directory") {
		return "Not a directory"
	}
	return errStr
}
