package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGetFakeHeader tests that buffers are exactly 4K and have sufficient entropy
func TestGetFakeHeader(t *testing.T) {
	buffer := getFakeHeader()

	if len(buffer) != bufferSize {
		t.Errorf("Expected buffer size %d, got %d", bufferSize, len(buffer))
	}

	entropy := calculateEntropy(buffer)
	minEntropy := 6.0 // Good entropy should be close to 8.0 for random data
	if entropy < minEntropy {
		t.Errorf("Buffer entropy too low: %.2f, expected at least %.2f", entropy, minEntropy)
	}
}

// TestGetFakeHeaderUniqueness tests that consecutive calls generate different buffers
func TestGetFakeHeaderUniqueness(t *testing.T) {
	buffer1 := getFakeHeader()
	buffer2 := getFakeHeader()
	buffer3 := getFakeHeader()

	same12 := true
	same23 := true
	for i := 0; i < 100 && i < len(buffer1); i++ {
		if buffer1[i] != buffer2[i] {
			same12 = false
		}
		if buffer2[i] != buffer3[i] {
			same23 = false
		}
	}

	if same12 && same23 {
		t.Error("Consecutive getFakeHeader() calls should generate different data")
	}
}

// TestBufferEntropy tests that generated buffers have good entropy
func TestBufferEntropy(t *testing.T) {
	for i := 0; i < 5; i++ {
		buffer := getFakeHeader()
		entropy := calculateEntropy(buffer)

		minEntropy := 6.0 // Realistic threshold for mixed pattern + random data
		maxEntropy := 8.0 // Theoretical maximum for bytes

		if entropy < minEntropy {
			t.Errorf("Buffer %d entropy too low: %.2f, expected at least %.2f", i, entropy, minEntropy)
		}

		if entropy > maxEntropy {
			t.Errorf("Buffer %d entropy impossibly high: %.2f, maximum is %.2f", i, entropy, maxEntropy)
		}

		t.Logf("Buffer %d entropy: %.2f", i, entropy)
	}
}

// TestTruncateFile tests the truncateFile function separately
func TestTruncateFile(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	testContent := "This content should be truncated to zero bytes."
	err := os.WriteFile(testFile, []byte(testContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	stat, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("Failed to stat test file: %v", err)
	}
	if stat.Size() == 0 {
		t.Error("File should have content before truncation")
	}

	success := truncateFile(testFile)
	if !success {
		t.Error("truncateFile should succeed")
	}

	stat, err = os.Stat(testFile)
	if err != nil {
		t.Fatalf("File should still exist after truncate: %v", err)
	}

	if stat.Size() != 0 {
		t.Errorf("File should be truncated to 0 bytes, got %d bytes", stat.Size())
	}

	// Verify content is empty
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read file after truncate: %v", err)
	}

	if len(content) != 0 {
		t.Errorf("File content should be empty, got %d bytes", len(content))
	}
}

// TestOverwriteWithRandomData tests file overwriting and truncation
func TestOverwriteWithRandomData(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	// Create test file with initial content
	initialContent := "This is test content that should be overwritten and then truncated."
	err := os.WriteFile(testFile, []byte(initialContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test overwrite function
	success := overwriteWithRandomData(testFile)
	if !success {
		t.Error("overwriteWithRandomData should succeed")
	}

	// Check file exists and is truncated (size 0)
	stat, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("File should still exist after overwrite: %v", err)
	}

	if stat.Size() != 0 {
		t.Errorf("File should be truncated to 0 bytes, got %d bytes", stat.Size())
	}

	// Verify content is empty
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read file after overwrite: %v", err)
	}

	if len(content) != 0 {
		t.Errorf("File content should be empty, got %d bytes", len(content))
	}
}

// TestCollectPathsRecursive tests recursive vs non-recursive behavior
func TestCollectPathsRecursive(t *testing.T) {
	// Create temp directory structure
	tempDir := t.TempDir()

	// Create test structure:
	// tempDir/
	//   file1.txt
	//   subdir/
	//     file2.txt
	//     deepdir/
	//       file3.txt

	file1 := filepath.Join(tempDir, "file1.txt")
	subdir := filepath.Join(tempDir, "subdir")
	file2 := filepath.Join(subdir, "file2.txt")
	deepdir := filepath.Join(subdir, "deepdir")
	file3 := filepath.Join(deepdir, "file3.txt")

	os.WriteFile(file1, []byte("content1"), 0644)
	os.Mkdir(subdir, 0755)
	os.WriteFile(file2, []byte("content2"), 0644)
	os.Mkdir(deepdir, 0755)
	os.WriteFile(file3, []byte("content3"), 0644)

	// Test non-recursive (should only get file1)
	*recursive = false
	var files, folders []string
	collectPaths(tempDir, &files, &folders)

	if len(files) != 0 {
		t.Errorf("Non-recursive should find 0 files in directory, got %d", len(files))
	}
	if len(folders) != 0 {
		t.Errorf("Non-recursive should find 0 folders in directory, got %d", len(folders))
	}

	// Test with file directly
	files = nil
	folders = nil
	collectPaths(file1, &files, &folders)

	if len(files) != 1 {
		t.Errorf("Should find 1 file when given file path, got %d", len(files))
	}

	// Test recursive (should get all files and folders)
	*recursive = true
	files = nil
	folders = nil
	collectPaths(tempDir, &files, &folders)

	if len(files) != 3 {
		t.Errorf("Recursive should find 3 files, got %d: %v", len(files), files)
	}
	if len(folders) != 3 { // tempDir, subdir, deepdir
		t.Errorf("Recursive should find 3 folders, got %d: %v", len(folders), folders)
	}
}

// TestGetSimpleError tests error message simplification
func TestGetSimpleError(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"open file.txt: no such file or directory", "No such file or directory"},
		{"mkdir test: permission denied", "Permission denied"},
		{"remove file.txt: is a directory", "Is a directory"},
		{"read dir: not a directory", "Not a directory"},
		{"some other error", "some other error"},
	}

	for _, test := range tests {
		// Create a mock error
		err := &mockError{msg: test.input}
		result := getSimpleError(err)

		if result != test.expected {
			t.Errorf("getSimpleError(%q) = %q, want %q", test.input, result, test.expected)
		}
	}
}

// TestIsSpecialFile tests special file detection
func TestIsSpecialFile(t *testing.T) {
	// Create temp directory and regular file
	tempDir := t.TempDir()
	regularFile := filepath.Join(tempDir, "regular.txt")

	err := os.WriteFile(regularFile, []byte("test"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test regular file
	info, err := os.Lstat(regularFile)
	if err != nil {
		t.Fatalf("Failed to stat regular file: %v", err)
	}

	if isSpecialFile(info) {
		t.Error("Regular file should not be considered special")
	}

	// Test directory
	dirInfo, err := os.Lstat(tempDir)
	if err != nil {
		t.Fatalf("Failed to stat directory: %v", err)
	}

	if isSpecialFile(dirInfo) {
		t.Error("Directory should not be considered special")
	}
}

// TestRenameToRandomName tests file renaming functionality
func TestRenameToRandomName(t *testing.T) {
	// Create temp directory and test file
	tempDir := t.TempDir()
	originalFile := filepath.Join(tempDir, "test.txt")

	err := os.WriteFile(originalFile, []byte("test content"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test rename
	newPath := renameToRandomName(originalFile)

	// Check original file no longer exists
	if _, err := os.Stat(originalFile); !os.IsNotExist(err) {
		t.Error("Original file should no longer exist after rename")
	}

	// Check new file exists
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("New file should exist at %s: %v", newPath, err)
	}

	// Check new filename is different and random-looking
	originalName := filepath.Base(originalFile)
	newName := filepath.Base(newPath)

	if originalName == newName {
		t.Error("New filename should be different from original")
	}

	if len(newName) != len(originalName) {
		t.Errorf("New filename should have same length as original (%d), got %d",
			len(originalName), len(newName))
	}

	// Check filename contains only hex characters
	for _, c := range newName {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("New filename should only contain hex characters, found %c in %s", c, newName)
		}
	}
}

// TestMinFunction tests the utility min function
func TestMinFunction(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{1, 2, 1},
		{5, 3, 3},
		{10, 10, 10},
		{0, 1, 0},
		{-1, 5, -1},
	}

	for _, test := range tests {
		result := min(test.a, test.b)
		if result != test.expected {
			t.Errorf("min(%d, %d) = %d, want %d", test.a, test.b, result, test.expected)
		}
	}
}

// Mock error type for testing
type mockError struct {
	msg string
}

func (e *mockError) Error() string {
	return e.msg
}

// calculateEntropy calculates Shannon entropy of byte data
// Returns value between 0 (no randomness) and 8 (perfect randomness for bytes)
func calculateEntropy(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}

	// Count frequency of each byte value
	freq := make(map[byte]int)
	for _, b := range data {
		freq[b]++
	}

	// Calculate entropy using Shannon entropy formula
	entropy := 0.0
	length := float64(len(data))

	for _, count := range freq {
		if count > 0 {
			p := float64(count) / length
			entropy -= p * math.Log2(p)
		}
	}

	return entropy
}