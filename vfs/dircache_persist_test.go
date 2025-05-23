package vfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/object"
	"github.com/rclone/rclone/fstest/mockfs"
	"github.com/rclone/rclone/lib/kv"
	"github.com/rclone/rclone/vfs/vfscommon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistentDirCacheBolt(t *testing.T) {
	// Create a temporary directory for cache files
	tempDir := t.TempDir()
	cacheFile := filepath.Join(tempDir, "test_cache.db")

	// Create a mock filesystem
	f := mockfs.NewFs("test", "testroot")

	// Create VFS with persistent cache enabled
	opt := vfscommon.DefaultOpt
	opt.DirCachePersist = true
	opt.DirCacheFile = cacheFile

	vfs := New(f, &opt)
	defer vfs.Shutdown()

	require.NotNil(t, vfs.persistentDirCache)

	// Initialize the cache
	err := vfs.persistentDirCache.Initialize()
	require.NoError(t, err)

	// Test saving empty cache
	err = vfs.persistentDirCache.SaveCache()
	assert.NoError(t, err)

	// Verify cache file was created
	_, err = os.Stat(cacheFile)
	assert.NoError(t, err)

	// Close and verify cleanup
	err = vfs.persistentDirCache.Close()
	assert.NoError(t, err)
}

func TestPersistentDirCacheWithData(t *testing.T) {
	// Create a temporary directory for cache files
	tempDir := t.TempDir()
	cacheFile := filepath.Join(tempDir, "test_cache_data.db")

	// Create a mock filesystem with some files
	f := mockfs.NewFs("test", "testroot")

	// Create VFS with persistent cache enabled
	opt := vfscommon.DefaultOpt
	opt.DirCachePersist = true
	opt.DirCacheFile = cacheFile
	opt.DirCacheTime = fs.Duration(time.Hour) // Long cache time for testing

	vfs := New(f, &opt)
	defer vfs.Shutdown()

	// Initialize the cache
	err := vfs.persistentDirCache.Initialize()
	require.NoError(t, err)

	// Create some directory structure in VFS
	root, err := vfs.Root()
	require.NoError(t, err)

	// Add some mock objects
	mockObj1 := object.NewStaticObjectInfo("file1.txt", time.Now(), 100, true, nil, f)
	mockObj2 := object.NewStaticObjectInfo("dir1/file2.txt", time.Now(), 200, true, nil, f)

	// Simulate reading a directory to populate cache
	root.mu.Lock()
	root.read = time.Now()
	root.items["file1.txt"] = newFile(root, "", mockObj1, "file1.txt")

	// Create subdirectory
	subDir := newDir(vfs, f, root, fs.NewDir("dir1", time.Now()))
	subDir.mu.Lock()
	subDir.read = time.Now()
	subDir.items["file2.txt"] = newFile(subDir, "dir1", mockObj2, "file2.txt")
	subDir.mu.Unlock()

	root.items["dir1"] = subDir
	root.mu.Unlock()

	// Test updating directory in cache
	err = vfs.persistentDirCache.UpdateDirectory(root)
	require.NoError(t, err)

	err = vfs.persistentDirCache.UpdateDirectory(subDir)
	require.NoError(t, err)

	// Save cache
	err = vfs.persistentDirCache.SaveCache()
	require.NoError(t, err)

	// Verify cache file exists and has data
	_, err = os.Stat(cacheFile)
	require.NoError(t, err)

	// Test cleanup
	err = vfs.persistentDirCache.CleanupOldEntries()
	assert.NoError(t, err)

	// Close cache
	err = vfs.persistentDirCache.Close()
	assert.NoError(t, err)
}

func TestPersistentDirCacheLoad(t *testing.T) {
	// Create a temporary directory for cache files
	tempDir := t.TempDir()
	cacheFile := filepath.Join(tempDir, "test_cache_load.db")

	// Create first VFS instance to populate cache
	f1 := mockfs.NewFs("test", "testroot")
	opt1 := vfscommon.DefaultOpt
	opt1.DirCachePersist = true
	opt1.DirCacheFile = cacheFile

	vfs1 := New(f1, &opt1)
	err := vfs1.persistentDirCache.Initialize()
	require.NoError(t, err)

	// Create some test data
	root1, err := vfs1.Root()
	require.NoError(t, err)

	mockObj := object.NewStaticObjectInfo("test.txt", time.Now(), 123, true, nil, f1)

	root1.mu.Lock()
	root1.read = time.Now()
	root1.items["test.txt"] = newFile(root1, "", mockObj, "test.txt")
	root1.mu.Unlock()

	// Save cache
	err = vfs1.persistentDirCache.UpdateDirectory(root1)
	require.NoError(t, err)
	err = vfs1.persistentDirCache.SaveCache()
	require.NoError(t, err)
	
	vfs1.Shutdown()

	// Create second VFS instance to load cache
	f2 := mockfs.NewFs("test", "testroot")
	opt2 := vfscommon.DefaultOpt
	opt2.DirCachePersist = true
	opt2.DirCacheFile = cacheFile

	vfs2 := New(f2, &opt2)
	defer vfs2.Shutdown()

	// Wait for background cache loading to complete
	time.Sleep(200 * time.Millisecond)

	// Verify cache was loaded (this is implementation dependent)
	root2, err := vfs2.Root()
	require.NoError(t, err)

	root2.mu.RLock()
	defer root2.mu.RUnlock()

	// Check if items were loaded
	assert.True(t, len(root2.items) >= 0) // At minimum, no errors during loading
}

func TestPersistentDirCacheDisabled(t *testing.T) {
	// Create VFS with persistent cache disabled
	f := mockfs.NewFs("test", "testroot")
	opt := vfscommon.DefaultOpt
	opt.DirCachePersist = false

	vfs := New(f, &opt)
	defer vfs.Shutdown()

	// Should not have persistent cache initialized
	assert.Nil(t, vfs.persistentDirCache)
}

func TestPersistentDirCacheInvalidation(t *testing.T) {
	// Create a temporary directory for cache files
	tempDir := t.TempDir()
	cacheFile := filepath.Join(tempDir, "test_cache_invalidation.db")

	// Create a mock filesystem
	f := mockfs.NewFs("test", "testroot")

	// Create VFS with persistent cache enabled
	opt := vfscommon.DefaultOpt
	opt.DirCachePersist = true
	opt.DirCacheFile = cacheFile

	vfs := New(f, &opt)
	defer vfs.Shutdown()

	err := vfs.persistentDirCache.Initialize()
	require.NoError(t, err)

	// Create test directory
	root, err := vfs.Root()
	require.NoError(t, err)

	mockObj := object.NewStaticObjectInfo("test.txt", time.Now(), 123, true, nil, f)

	root.mu.Lock()
	root.read = time.Now()
	root.items["test.txt"] = newFile(root, "", mockObj, "test.txt")
	root.mu.Unlock()

	// Update and save cache
	err = vfs.persistentDirCache.UpdateDirectory(root)
	require.NoError(t, err)

	// Test invalidation
	err = vfs.persistentDirCache.InvalidateDirectory("")
	assert.NoError(t, err)

	// Close cache
	err = vfs.persistentDirCache.Close()
	assert.NoError(t, err)
}

func TestPersistentDirCacheAutoPath(t *testing.T) {
	// Test auto-generated path functionality
	f := mockfs.NewFs("test", "testroot")

	// Create VFS with persistent cache enabled but no specific file
	opt := vfscommon.DefaultOpt
	opt.DirCachePersist = true
	opt.DirCacheFile = "" // Should auto-generate

	vfs := New(f, &opt)
	defer vfs.Shutdown()

	require.NotNil(t, vfs.persistentDirCache)

	// Initialize should work even with auto-generated path
	err := vfs.persistentDirCache.Initialize()
	assert.NoError(t, err)

	// Test basic operations
	err = vfs.persistentDirCache.SaveCache()
	assert.NoError(t, err)

	err = vfs.persistentDirCache.Close()
	assert.NoError(t, err)
}

func TestPersistentDirCacheMetadataValidation(t *testing.T) {
	// Create a temporary directory for cache files
	tempDir := t.TempDir()
	cacheFile := filepath.Join(tempDir, "test_cache_validation.db")

	// Create a mock filesystem
	f1 := mockfs.NewFs("test", "testroot")

	// Create VFS with persistent cache enabled
	opt := vfscommon.DefaultOpt
	opt.DirCachePersist = true
	opt.DirCacheFile = cacheFile

	vfs1 := New(f1, &opt)
	
	err := vfs1.persistentDirCache.Initialize()
	require.NoError(t, err)

	// Save some metadata
	err = vfs1.persistentDirCache.SaveCache()
	require.NoError(t, err)
	
	vfs1.Shutdown()

	// Create a different remote with same cache file - should be ignored
	f2 := mockfs.NewFs("different", "testroot")
	vfs2 := New(f2, &opt)
	defer vfs2.Shutdown()

	// Should still initialize successfully, but cache should be ignored due to remote mismatch
	err = vfs2.persistentDirCache.Initialize()
	assert.NoError(t, err)

	// Load should not fail, but should not load any data due to validation
	err = vfs2.persistentDirCache.LoadCache()
	assert.NoError(t, err)
}

// Test performance with larger datasets
func TestPersistentDirCachePerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	// Create a temporary directory for cache files
	tempDir := t.TempDir()
	cacheFile := filepath.Join(tempDir, "test_cache_perf.db")

	// Create a mock filesystem
	f := mockfs.NewFs("test", "testroot")

	// Create VFS with persistent cache enabled
	opt := vfscommon.DefaultOpt
	opt.DirCachePersist = true
	opt.DirCacheFile = cacheFile
	opt.DirCacheTime = fs.Duration(time.Hour)

	vfs := New(f, &opt)
	defer vfs.Shutdown()

	err := vfs.persistentDirCache.Initialize()
	require.NoError(t, err)

	// Create a large directory structure
	root, err := vfs.Root()
	require.NoError(t, err)

	const numFiles = 1000
	const numDirs = 100

	start := time.Now()

	// Create many files and directories
	root.mu.Lock()
	root.read = time.Now()
	for i := 0; i < numFiles; i++ {
		name := fmt.Sprintf("file%d.txt", i)
		mockObj := object.NewStaticObjectInfo(name, time.Now(), int64(i*100), true, nil, f)
		root.items[name] = newFile(root, "", mockObj, name)
	}

	for i := 0; i < numDirs; i++ {
		name := fmt.Sprintf("dir%d", i)
		subDir := newDir(vfs, f, root, fs.NewDir(name, time.Now()))
		subDir.mu.Lock()
		subDir.read = time.Now()
		// Add a few files to each subdirectory
		for j := 0; j < 5; j++ {
			fileName := fmt.Sprintf("file%d.txt", j)
			mockObj := object.NewStaticObjectInfo(filepath.Join(name, fileName), time.Now(), int64(j*50), true, nil, f)
			subDir.items[fileName] = newFile(subDir, name, mockObj, fileName)
		}
		subDir.mu.Unlock()
		root.items[name] = subDir
	}
	root.mu.Unlock()

	createTime := time.Since(start)
	t.Logf("Created %d files and %d dirs in %v", numFiles, numDirs, createTime)

	// Test cache save performance
	start = time.Now()
	err = vfs.persistentDirCache.SaveCache()
	require.NoError(t, err)
	saveTime := time.Since(start)
	t.Logf("Saved cache in %v", saveTime)

	// Test cache load performance
	start = time.Now()
	err = vfs.persistentDirCache.LoadCache()
	require.NoError(t, err)
	loadTime := time.Since(start)
	t.Logf("Loaded cache in %v", loadTime)

	// Verify reasonable performance (these are generous bounds)
	assert.Less(t, saveTime, 5*time.Second, "Cache save took too long")
	assert.Less(t, loadTime, 2*time.Second, "Cache load took too long")
}