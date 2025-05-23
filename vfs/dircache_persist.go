package vfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/lib/kv"
)

// PersistentDirCache handles saving and loading directory cache using BoltDB
type PersistentDirCache struct {
	vfs         *VFS
	mu          sync.RWMutex
	db          *kv.DB
	initialized bool
}

// CachedDirEntry represents a directory entry that can be serialized
type CachedDirEntry struct {
	Name      string             `json:"name"`
	Size      int64              `json:"size"`
	ModTime   time.Time          `json:"modTime"`
	IsDir     bool               `json:"isDir"`
	IsVirtual bool               `json:"isVirtual,omitempty"`
	Remote    string             `json:"remote"`     // Full remote path
	MimeType  string             `json:"mimeType,omitempty"`
	Hashes    map[string]string  `json:"hashes,omitempty"`
	Metadata  map[string]string  `json:"metadata,omitempty"`
	ID        string             `json:"id,omitempty"` // Directory ID if available
}

// CachedDir represents a cached directory with its entries
type CachedDir struct {
	Path     string           `json:"path"`
	ModTime  time.Time        `json:"modTime"`
	ReadTime time.Time        `json:"readTime"`
	Entries  []CachedDirEntry `json:"entries"`
}

// CacheMetadata stores metadata about the cache
type CacheMetadata struct {
	Version    int       `json:"version"`
	SaveTime   time.Time `json:"saveTime"`
	RemoteInfo string    `json:"remoteInfo"`
}

const (
	dirCacheVersion = 1
	metadataKey     = "cache_metadata"
)

// CachedFsObject implements fs.Object using cached metadata
// This creates a real rclone object that will fetch content on-demand
type CachedFsObject struct {
	fs       fs.Fs
	remote   string
	size     int64
	modTime  time.Time
	mimeType string
	hashes   map[string]string
	metadata map[string]string
}

// Fs returns the filesystem this object is part of
func (o *CachedFsObject) Fs() fs.Info { return o.fs }

// String returns a description of the Object
func (o *CachedFsObject) String() string { return o.remote }

// Remote returns the remote path
func (o *CachedFsObject) Remote() string { return o.remote }

// ModTime returns the modification date of the file
func (o *CachedFsObject) ModTime(ctx context.Context) time.Time { return o.modTime }

// Size returns the size of the file
func (o *CachedFsObject) Size() int64 { return o.size }

// Hash returns the requested hash of the file
func (o *CachedFsObject) Hash(ctx context.Context, ht hash.Type) (string, error) {
	if o.hashes != nil {
		if h, ok := o.hashes[ht.String()]; ok {
			return h, nil
		}
	}
	return "", hash.ErrUnsupported
}

// Storable returns whether the object is storable
func (o *CachedFsObject) Storable() bool { return true }

// SetModTime sets the metadata on the object to set the modification date
func (o *CachedFsObject) SetModTime(ctx context.Context, t time.Time) error {
	// Delegate to real object fetch
	realObj, err := o.fs.NewObject(ctx, o.remote)
	if err != nil {
		return err
	}
	return realObj.SetModTime(ctx, t)
}

// Open opens the file for read
func (o *CachedFsObject) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	// This will trigger fetching the real object from remote
	realObj, err := o.fs.NewObject(ctx, o.remote)
	if err != nil {
		return nil, err
	}
	return realObj.Open(ctx, options...)
}

// Update in to the object with the modTime given of the given size
func (o *CachedFsObject) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	// Delegate to real object
	realObj, err := o.fs.NewObject(ctx, o.remote)
	if err != nil {
		return err
	}
	return realObj.Update(ctx, in, src, options...)
}

// Remove this object
func (o *CachedFsObject) Remove(ctx context.Context) error {
	// Delegate to real object
	realObj, err := o.fs.NewObject(ctx, o.remote)
	if err != nil {
		return err
	}
	return realObj.Remove(ctx)
}

// MimeType returns the content type of the Object if known
func (o *CachedFsObject) MimeType(ctx context.Context) string {
	return o.mimeType
}

// Metadata returns metadata for an object
func (o *CachedFsObject) Metadata(ctx context.Context) (fs.Metadata, error) {
	if o.metadata != nil {
		return fs.Metadata(o.metadata), nil
	}
	return nil, nil
}

// NewCachedFsObject creates a new CachedFsObject from cache data
func NewCachedFsObject(f fs.Fs, entry CachedDirEntry) *CachedFsObject {
	return &CachedFsObject{
		fs:       f,
		remote:   entry.Remote,
		size:     entry.Size,
		modTime:  entry.ModTime,
		mimeType: entry.MimeType,
		hashes:   entry.Hashes,
		metadata: entry.Metadata,
	}
}

// LoadDirectoryFromCache loads a specific directory from cache if available
func (cache *PersistentDirCache) LoadDirectoryFromCache(dir *Dir) bool {
	if !cache.initialized {
		return false
	}
	
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	
	op := &loadDirectoryOperation{
		vfs: cache.vfs,
		dir: dir,
	}
	
	if err := cache.db.Do(false, op); err != nil {
		fs.Debugf(cache.vfs.f, "Failed to load directory %s from cache: %v", dir.path, err)
		return false
	}
	
	return op.loaded
}

// LoadDirEntries loads raw directory entries from cache
func (cache *PersistentDirCache) LoadDirEntries(path string) (entries fs.DirEntries, found bool) {
	if !cache.initialized {
		fs.Debugf(cache.vfs.f, "LoadDirEntries(%s): cache not initialized", path)
		return nil, false
	}
	
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	
	fs.Debugf(cache.vfs.f, "LoadDirEntries(%s): attempting to load from cache", path)
	
	op := &loadEntriesOperation{
		vfs:  cache.vfs,
		path: path,
	}
	
	if err := cache.db.Do(false, op); err != nil {
		fs.Debugf(cache.vfs.f, "Failed to load entries for %s from cache: %v", path, err)
		return nil, false
	}
	
	if op.found {
		fs.Infof(cache.vfs.f, "LoadDirEntries(%s): loaded %d entries from cache", path, len(op.entries))
	} else {
		fs.Debugf(cache.vfs.f, "LoadDirEntries(%s): not found in cache", path)
	}
	
	return op.entries, op.found
}

// SaveDirEntries saves raw directory entries to cache
func (cache *PersistentDirCache) SaveDirEntries(path string, entries fs.DirEntries) {
	if !cache.initialized {
		fs.Debugf(cache.vfs.f, "SaveDirEntries(%s): cache not initialized", path)
		return
	}
	
	fs.Infof(cache.vfs.f, "SaveDirEntries(%s): saving %d entries to cache", path, len(entries))
	
	// Clone the entries to avoid race conditions
	entriesCopy := make(fs.DirEntries, len(entries))
	copy(entriesCopy, entries)
	
	go func() {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		
		op := &saveEntriesOperation{
			vfs:     cache.vfs,
			path:    path,
			entries: entriesCopy,
		}
		
		if err := cache.db.Do(true, op); err != nil {
			fs.Errorf(cache.vfs.f, "Failed to save entries for %s to cache: %v", path, err)
		} else {
			fs.Infof(cache.vfs.f, "SaveDirEntries(%s): successfully saved to cache", path)
		}
	}()
}

// loadDirectoryOperation implements kv.Op for loading a single directory
type loadDirectoryOperation struct {
	vfs    *VFS
	dir    *Dir
	loaded bool
}

func (op *loadDirectoryOperation) Do(ctx context.Context, bucket kv.Bucket) error {
	dirKey := "dir:" + op.dir.path
	data := bucket.Get([]byte(dirKey))
	if data == nil {
		return nil // Directory not in cache
	}
	
	var cachedDir CachedDir
	if err := json.Unmarshal(data, &cachedDir); err != nil {
		return err
	}
	
	// Restore directory entries
	op.dir.items = make(map[string]Node)
	if op.dir.virtual != nil {
		op.dir.virtual = make(map[string]vState)
	}
	
	for _, entry := range cachedDir.Entries {
		if entry.IsVirtual {
			continue
		}
		
		if entry.IsDir {
			// Create directory entry
			fsDir := fs.NewDir(filepath.Join(cachedDir.Path, entry.Name), entry.ModTime)
			childDir := newDir(op.vfs, op.vfs.f, op.dir, fsDir)
			op.dir.items[entry.Name] = childDir
		} else if entry.Remote != "" {
			// Create file object from cached metadata
			cachedObj := NewCachedFsObject(op.vfs.f, entry)
			file := newFile(op.dir, op.dir.path, cachedObj, entry.Name)
			op.dir.items[entry.Name] = file
		}
	}
	
	op.loaded = true
	fs.Debugf(op.vfs.f, "Loaded %d items for directory %s from cache", len(op.dir.items), op.dir.path)
	return nil
}

// loadEntriesOperation implements kv.Op for loading directory entries
type loadEntriesOperation struct {
	vfs     *VFS
	path    string
	entries fs.DirEntries
	found   bool
}

func (op *loadEntriesOperation) Do(ctx context.Context, bucket kv.Bucket) error {
	entriesKey := "entries:" + op.path
	data := bucket.Get([]byte(entriesKey))
	if data == nil {
		return nil
	}
	
	// Unmarshal the cached entries
	var cachedEntries []CachedDirEntry
	if err := json.Unmarshal(data, &cachedEntries); err != nil {
		return err
	}
	
	// Convert back to fs.DirEntries
	op.entries = make(fs.DirEntries, 0, len(cachedEntries))
	for _, ce := range cachedEntries {
		if ce.IsDir {
			// Create directory entry
			op.entries = append(op.entries, fs.NewDir(ce.Remote, ce.ModTime))
		} else {
			// Create file entry using CachedFsObject
			obj := NewCachedFsObject(op.vfs.f, ce)
			op.entries = append(op.entries, obj)
		}
	}
	
	op.found = true
	fs.Debugf(op.vfs.f, "Loaded %d cached entries for directory %s", len(op.entries), op.path)
	return nil
}

// saveEntriesOperation implements kv.Op for saving directory entries
type saveEntriesOperation struct {
	vfs     *VFS
	path    string
	entries fs.DirEntries
}

func (op *saveEntriesOperation) Do(ctx context.Context, bucket kv.Bucket) error {
	// Convert DirEntries to cacheable format
	cachedEntries := make([]CachedDirEntry, 0, len(op.entries))
	
	for _, entry := range op.entries {
		ce := CachedDirEntry{
			Name:    path.Base(entry.Remote()),
			Remote:  entry.Remote(),
			Size:    entry.Size(),
			ModTime: entry.ModTime(ctx),
		}
		
		// Determine if it's a directory and get ID if available
		if dir, ok := entry.(fs.Directory); ok {
			ce.IsDir = true
			ce.ID = dir.ID()
		} else {
			ce.IsDir = false
		}
		
		// If it's an object, get additional metadata
		if obj, ok := entry.(fs.Object); ok && !ce.IsDir {
			// Get MimeType if available
			if mimeTyper, ok := obj.(fs.MimeTyper); ok {
				ce.MimeType = mimeTyper.MimeType(ctx)
			}
			
			// Get hashes if available
			ce.Hashes = make(map[string]string)
			for _, ht := range hash.Supported().Array() {
				if h, err := obj.Hash(ctx, ht); err == nil && h != "" {
					ce.Hashes[ht.String()] = h
				}
			}
			
			// Get metadata if available
			if metadataer, ok := obj.(fs.Metadataer); ok {
				if meta, err := metadataer.Metadata(ctx); err == nil && meta != nil {
					ce.Metadata = make(map[string]string)
					for k, v := range meta {
						ce.Metadata[k] = v
					}
				}
			}
		}
		
		cachedEntries = append(cachedEntries, ce)
	}
	
	// Marshal and save
	data, err := json.Marshal(cachedEntries)
	if err != nil {
		return fmt.Errorf("failed to marshal entries: %w", err)
	}
	
	entriesKey := "entries:" + op.path
	if err := bucket.Put([]byte(entriesKey), data); err != nil {
		return fmt.Errorf("failed to save entries: %w", err)
	}
	
	fs.Debugf(op.vfs.f, "Saved %d entries for directory %s to cache", len(cachedEntries), op.path)
	return nil
}

// saveOperation implements kv.Op for saving cache data
type saveOperation struct {
	vfs *VFS
}

func (op *saveOperation) Do(ctx context.Context, bucket kv.Bucket) error {
	// Save metadata
	metadata := CacheMetadata{
		Version:    dirCacheVersion,
		SaveTime:   time.Now(),
		RemoteInfo: fs.ConfigString(op.vfs.f),
	}
	
	metadataData, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	
	if err := bucket.Put([]byte(metadataKey), metadataData); err != nil {
		return fmt.Errorf("failed to save metadata: %w", err)
	}
	
	// Save directories
	dirCount := 0
	op.vfs.root.walk(func(d *Dir) {
		d.mu.RLock()
		defer d.mu.RUnlock()
		
		// Skip if directory hasn't been read or is too old
		if d.read.IsZero() || time.Since(d.read) > time.Duration(op.vfs.Opt.DirCacheTime) {
			return
		}
		
		cachedDir := CachedDir{
			Path:     d.path,
			ModTime:  d.modTime,
			ReadTime: d.read,
			Entries:  make([]CachedDirEntry, 0, len(d.items)),
		}
		
		// Convert directory items to serializable format
		fs.Debugf(op.vfs.f, "Saving directory %s with %d items", d.path, len(d.items))
		for name, node := range d.items {
			entry := CachedDirEntry{
				Name:    name,
				Size:    node.Size(),
				ModTime: node.ModTime(),
				IsDir:   node.IsDir(),
			}
			
			fs.Debugf(op.vfs.f, "Saving item: %s (isDir: %v, size: %d)", name, entry.IsDir, entry.Size)
			
			// Store complete metadata for files
			if !node.IsDir() {
				if file, ok := node.(*File); ok {
					// Always store the remote path for files
					if d.path == "" {
						entry.Remote = entry.Name
					} else {
						entry.Remote = d.path + "/" + entry.Name
					}
					
					// If we have an fs.Object, store additional metadata
					if file.o != nil {
						ctx := context.Background()
						
						// Get MimeType if available
						if mimeTyper, ok := file.o.(fs.MimeTyper); ok {
							entry.MimeType = mimeTyper.MimeType(ctx)
						}
						
						// Store hashes
						entry.Hashes = make(map[string]string)
						for _, hashType := range hash.Supported().Array() {
							if h, err := file.o.Hash(ctx, hashType); err == nil && h != "" {
								entry.Hashes[hashType.String()] = h
							}
						}
						
						// Store metadata if supported
						if metadataer, ok := file.o.(fs.Metadataer); ok {
							if meta, err := metadataer.Metadata(ctx); err == nil && meta != nil {
								entry.Metadata = make(map[string]string)
								for k, v := range meta {
									entry.Metadata[k] = v
								}
							}
						}
					}
				}
			}
			
			// Check if it's a virtual entry
			if d.virtual != nil {
				if state, exists := d.virtual[name]; exists && state != vOK {
					entry.IsVirtual = true
				}
			}
			
			cachedDir.Entries = append(cachedDir.Entries, entry)
		}
		
		// Serialize and store directory
		dirData, err := json.Marshal(cachedDir)
		if err != nil {
			fs.Errorf(op.vfs.f, "Failed to marshal directory %s: %v", d.path, err)
			return
		}
		
		dirKey := fmt.Sprintf("dir:%s", d.path)
		if err := bucket.Put([]byte(dirKey), dirData); err != nil {
			fs.Errorf(op.vfs.f, "Failed to save directory %s: %v", d.path, err)
			return
		}
		
		dirCount++
	})
	
	fs.Debugf(op.vfs.f, "Successfully saved %d directories to cache", dirCount)
	return nil
}

// loadOperation implements kv.Op for loading cache data
type loadOperation struct {
	vfs *VFS
}

func (op *loadOperation) Do(ctx context.Context, bucket kv.Bucket) error {
	// Count total entries first to check if cache has data
	cursor := bucket.Cursor()
	totalEntries := 0
	dirEntries := 0
	for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
		totalEntries++
		if strings.HasPrefix(string(key), "dir:") {
			dirEntries++
		}
	}
	
	fs.Debugf(op.vfs.f, "Cache scan: %d total entries, %d directory entries", totalEntries, dirEntries)
	
	if dirEntries == 0 {
		fs.Debugf(op.vfs.f, "No directory entries found in cache")
		return nil
	}
	
	// Load and validate metadata (optional, continue even if missing)
	metadataData := bucket.Get([]byte(metadataKey))
	if metadataData != nil {
		var metadata CacheMetadata
		if err := json.Unmarshal(metadataData, &metadata); err != nil {
			fs.Debugf(op.vfs.f, "Failed to unmarshal cache metadata: %v, proceeding anyway", err)
		} else {
			// Validate cache version
			if metadata.Version != dirCacheVersion {
				fs.Debugf(op.vfs.f, "Cache version mismatch (got %d, expected %d), proceeding anyway", 
					metadata.Version, dirCacheVersion)
			}
			
			// Validate remote info
			currentRemote := fs.ConfigString(op.vfs.f)
			if metadata.RemoteInfo != currentRemote {
				fs.Debugf(op.vfs.f, "Remote configuration changed, proceeding anyway")
			}
			
			// Check if cache is too old
			maxAge := time.Duration(op.vfs.Opt.DirCacheTime) * 2
			if time.Since(metadata.SaveTime) > maxAge {
				fs.Debugf(op.vfs.f, "Cache too old (%v), proceeding anyway", time.Since(metadata.SaveTime))
			}
		}
	} else {
		fs.Debugf(op.vfs.f, "No cache metadata found, but proceeding with directory loading")
	}
	
	// Load ALL directories from cache - they're just metadata, not file content
	loadedDirs := 0
	cursor = bucket.Cursor()
	
	for key, data := cursor.First(); key != nil; key, data = cursor.Next() {
		keyStr := string(key)
		if !strings.HasPrefix(keyStr, "dir:") {
			continue
		}
		
		var cachedDir CachedDir
		if err := json.Unmarshal(data, &cachedDir); err != nil {
			fs.Errorf(op.vfs.f, "Failed to unmarshal directory %s: %v", keyStr, err)
			continue
		}
		
		if loadedDirs%10 == 0 {
			fs.Debugf(op.vfs.f, "Loading directory %d: %s", loadedDirs, cachedDir.Path)
		}
		
		if err := op.restoreDirectory(cachedDir); err != nil {
			fs.Errorf(op.vfs.f, "Failed to restore directory %s: %v", cachedDir.Path, err)
			continue
		}
		
		loadedDirs++
	}
	
	fs.Debugf(op.vfs.f, "Successfully loaded %d directories from cache", loadedDirs)
	return nil
}

// restoreDirectory restores a single directory from cache
func (op *loadOperation) restoreDirectory(cachedDir CachedDir) error {
	// Find or create the directory node
	dir, err := op.vfs.mkdirAll(cachedDir.Path, 0755)
	if err != nil {
		return fmt.Errorf("failed to create directory path: %w", err)
	}
	
	dir.mu.Lock()
	defer dir.mu.Unlock()
	
	// Always restore from cache on mount startup (ignore existing read time)
	fs.Debugf(op.vfs.f, "Restoring directory '%s' with %d entries from cache", cachedDir.Path, len(cachedDir.Entries))
	
	// Restore all entries as native rclone objects
	dir.items = make(map[string]Node)
	if dir.virtual != nil {
		dir.virtual = make(map[string]vState)
	}
	
	// Restore directory entries and cache file metadata
	for _, entry := range cachedDir.Entries {
		if entry.IsVirtual {
			// Skip virtual entries for now - they'll be recreated as needed
			continue
		}
		
		if entry.IsDir {
			// Create directory entry immediately
			fsDir := fs.NewDir(filepath.Join(cachedDir.Path, entry.Name), entry.ModTime)
			childDir := newDir(op.vfs, op.vfs.f, dir, fsDir)
			dir.items[entry.Name] = childDir
		} else if !entry.IsVirtual && entry.Remote != "" {
			// Create native rclone fs.Object from cached metadata
			cachedObj := NewCachedFsObject(op.vfs.f, entry)
			file := newFile(dir, dir.path, cachedObj, entry.Name)
			dir.items[entry.Name] = file
			
			fs.Debugf(op.vfs.f, "Restored file '%s' with size %d from cache", entry.Name, entry.Size)
		}
	}
	
	// Update directory metadata
	dir.read = time.Now()
	dir.modTime = cachedDir.ModTime
	
	return nil
}

// deleteOperation implements kv.Op for deleting cache entries
type deleteOperation struct {
	path string
}

func (op *deleteOperation) Do(ctx context.Context, bucket kv.Bucket) error {
	dirKey := fmt.Sprintf("dir:%s", op.path)
	return bucket.Delete([]byte(dirKey))
}

// updateOperation implements kv.Op for updating a directory
type updateOperation struct {
	dir *Dir
}

func (op *updateOperation) Do(ctx context.Context, bucket kv.Bucket) error {
	op.dir.mu.RLock()
	defer op.dir.mu.RUnlock()
	
	// Only update if directory has been read recently
	if op.dir.read.IsZero() || time.Since(op.dir.read) > time.Duration(op.dir.vfs.Opt.DirCacheTime) {
		return nil
	}
	
	cachedDir := CachedDir{
		Path:     op.dir.path,
		ModTime:  op.dir.modTime,
		ReadTime: op.dir.read,
		Entries:  make([]CachedDirEntry, 0, len(op.dir.items)),
	}
	
	// Convert directory items to serializable format
	for name, node := range op.dir.items {
		entry := CachedDirEntry{
			Name:    name,
			Size:    node.Size(),
			ModTime: node.ModTime(),
			IsDir:   node.IsDir(),
		}
		
		// Check if it's a virtual entry
		if op.dir.virtual != nil {
			if state, exists := op.dir.virtual[name]; exists && state != vOK {
				entry.IsVirtual = true
			}
		}
		
		cachedDir.Entries = append(cachedDir.Entries, entry)
	}
	
	// Serialize and store directory
	dirData, err := json.Marshal(cachedDir)
	if err != nil {
		return fmt.Errorf("failed to marshal directory: %w", err)
	}
	
	dirKey := fmt.Sprintf("dir:%s", op.dir.path)
	return bucket.Put([]byte(dirKey), dirData)
}

// cleanupOperation implements kv.Op for cleaning up old entries
type cleanupOperation struct {
	vfs *VFS
}

func (op *cleanupOperation) Do(ctx context.Context, bucket kv.Bucket) error {
	maxAge := time.Duration(op.vfs.Opt.DirCacheTime) * 3 // Keep cache entries 3x the cache time
	var toDelete [][]byte
	
	cursor := bucket.Cursor()
	for key, data := cursor.First(); key != nil; key, data = cursor.Next() {
		keyStr := string(key)
		if !strings.HasPrefix(keyStr, "dir:") {
			continue
		}
		
		var cachedDir CachedDir
		if err := json.Unmarshal(data, &cachedDir); err != nil {
			// If we can't unmarshal, mark for deletion
			toDelete = append(toDelete, key)
			continue
		}
		
		// Delete if too old
		if time.Since(cachedDir.ReadTime) > maxAge {
			toDelete = append(toDelete, key)
		}
	}
	
	// Delete old entries
	for _, key := range toDelete {
		if err := bucket.Delete(key); err != nil {
			fs.Errorf(op.vfs.f, "Failed to delete old cache entry %s: %v", string(key), err)
		}
	}
	
	if len(toDelete) > 0 {
		fs.Debugf(op.vfs.f, "Cleaned up %d old cache entries", len(toDelete))
	}
	
	return nil
}

// NewPersistentDirCache creates a new persistent directory cache using BoltDB
func NewPersistentDirCache(vfs *VFS) *PersistentDirCache {
	return &PersistentDirCache{
		vfs: vfs,
	}
}

// Initialize sets up the BoltDB database
func (pdc *PersistentDirCache) Initialize() error {
	if !pdc.vfs.Opt.DirCachePersist {
		return nil
	}
	
	pdc.mu.Lock()
	defer pdc.mu.Unlock()
	
	if pdc.initialized {
		return nil
	}
	
	// Open database using the VFS filesystem as the key
	db, err := kv.Start(context.Background(), "dircache", pdc.vfs.f)
	if err != nil {
		return fmt.Errorf("failed to open cache database: %w", err)
	}
	
	pdc.db = db
	pdc.initialized = true
	
	fs.Debugf(pdc.vfs.f, "Initialized persistent directory cache")
	return nil
}

// Close closes the database connection
func (pdc *PersistentDirCache) Close() error {
	pdc.mu.Lock()
	defer pdc.mu.Unlock()
	
	if pdc.db != nil {
		err := pdc.db.Stop(false)
		pdc.db = nil
		pdc.initialized = false
		return err
	}
	return nil
}

// SaveCache saves the current directory cache to the database
func (pdc *PersistentDirCache) SaveCache() error {
	if !pdc.vfs.Opt.DirCachePersist {
		return nil
	}
	
	if err := pdc.Initialize(); err != nil {
		return err
	}
	
	pdc.mu.RLock()
	defer pdc.mu.RUnlock()
	
	if !pdc.initialized {
		return fmt.Errorf("cache database not initialized")
	}
	
	fs.Debugf(pdc.vfs.f, "Saving directory cache to database")
	
	saveOp := &saveOperation{vfs: pdc.vfs}
	return pdc.db.Do(true, saveOp)
}

// LoadCache loads the directory cache from the database
func (pdc *PersistentDirCache) LoadCache() error {
	if !pdc.vfs.Opt.DirCachePersist {
		return nil
	}
	
	if err := pdc.Initialize(); err != nil {
		return err
	}
	
	pdc.mu.RLock()
	defer pdc.mu.RUnlock()
	
	if !pdc.initialized {
		return fmt.Errorf("cache database not initialized")
	}
	
	fs.Debugf(pdc.vfs.f, "Loading directory cache from database")
	
	loadOp := &loadOperation{vfs: pdc.vfs}
	return pdc.db.Do(false, loadOp)
}

// InvalidateDirectory removes a directory from the persistent cache
func (pdc *PersistentDirCache) InvalidateDirectory(path string) error {
	if !pdc.vfs.Opt.DirCachePersist || !pdc.initialized {
		return nil
	}
	
	pdc.mu.RLock()
	defer pdc.mu.RUnlock()
	
	deleteOp := &deleteOperation{path: path}
	return pdc.db.Do(true, deleteOp)
}

// UpdateDirectory updates a directory in the persistent cache
func (pdc *PersistentDirCache) UpdateDirectory(dir *Dir) error {
	if !pdc.vfs.Opt.DirCachePersist || !pdc.initialized {
		return nil
	}
	
	pdc.mu.RLock()
	defer pdc.mu.RUnlock()
	
	updateOp := &updateOperation{dir: dir}
	return pdc.db.Do(true, updateOp)
}

// CleanupOldEntries removes old cache entries
func (pdc *PersistentDirCache) CleanupOldEntries() error {
	if !pdc.vfs.Opt.DirCachePersist || !pdc.initialized {
		return nil
	}
	
	pdc.mu.RLock()
	defer pdc.mu.RUnlock()
	
	cleanupOp := &cleanupOperation{vfs: pdc.vfs}
	return pdc.db.Do(true, cleanupOp)
}