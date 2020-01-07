// Code generated for package app by go-bindata DO NOT EDIT. (@generated)
// sources:
// migrations/201911252319_init.down.sql
// migrations/201911252319_init.up.sql
package app

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func bindataRead(data []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewBuffer(data))
	if err != nil {
		return nil, fmt.Errorf("Read %q: %v", name, err)
	}

	var buf bytes.Buffer
	_, err = io.Copy(&buf, gz)
	clErr := gz.Close()

	if err != nil {
		return nil, fmt.Errorf("Read %q: %v", name, err)
	}
	if clErr != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

type asset struct {
	bytes []byte
	info  os.FileInfo
}

type bindataFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

// Name return file name
func (fi bindataFileInfo) Name() string {
	return fi.name
}

// Size return file size
func (fi bindataFileInfo) Size() int64 {
	return fi.size
}

// Mode return file mode
func (fi bindataFileInfo) Mode() os.FileMode {
	return fi.mode
}

// Mode return file modify time
func (fi bindataFileInfo) ModTime() time.Time {
	return fi.modTime
}

// IsDir return file whether a directory
func (fi bindataFileInfo) IsDir() bool {
	return fi.mode&os.ModeDir != 0
}

// Sys return file is sys mode
func (fi bindataFileInfo) Sys() interface{} {
	return nil
}

var __201911252319_initDownSql = []byte("\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\xff\x72\x09\xf2\x0f\x50\x08\x71\x74\xf2\x71\x55\x28\x49\x4d\xcc\x8d\x4f\xce\xcf\x4b\xcb\x4c\x2f\xb6\xe6\x02\x4b\x78\xfa\xb9\xb8\x46\x28\xe4\x24\x16\x97\xc4\x17\xa5\x16\xe4\x17\x95\xc4\x67\xa6\x54\x40\xe5\x20\x9a\xd2\x53\x8b\x8a\x32\x4b\xe2\x4b\x8b\x53\x8b\x50\x35\x95\x16\xa4\x24\x96\xa4\xa6\xc4\x27\x62\xea\xc9\xcc\xcb\xc9\xcc\x4b\x8d\x4f\xce\xcf\xcd\x4d\xcd\x2b\x29\xb6\xe6\x02\x04\x00\x00\xff\xff\xbd\x25\xed\x8b\x85\x00\x00\x00")

func _201911252319_initDownSqlBytes() ([]byte, error) {
	return bindataRead(
		__201911252319_initDownSql,
		"201911252319_init.down.sql",
	)
}

func _201911252319_initDownSql() (*asset, error) {
	bytes, err := _201911252319_initDownSqlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "201911252319_init.down.sql", size: 133, mode: os.FileMode(420), modTime: time.Unix(1574794364, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var __201911252319_initUpSql = []byte("\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\xff\x6c\xd1\xc1\x4e\x84\x40\x0c\x06\xe0\x33\xf3\x14\x3d\xee\x26\xfb\x06\x9e\x50\xe7\xb0\x11\x59\x43\x30\x71\x4f\x4d\x03\x75\x9d\xc8\x0c\x64\x28\x66\x7d\x7b\xe3\x2e\x6a\x65\xb8\x15\xda\xf9\xf3\x35\xbd\xab\x6c\x5e\x5b\xa8\xf3\xdb\xc2\xc2\x89\x63\x74\x82\xd3\xc8\x71\x84\x8d\xc9\xd4\x77\x20\xcf\x20\x7c\x16\x28\x0f\x35\x94\xcf\x45\xb1\x33\x59\xf3\x46\x82\xae\x4d\xfe\x77\x34\x0a\x46\x1e\xfa\x28\x20\xce\xf3\x28\xe4\x87\x9d\xc9\x9e\xaa\xfd\x63\x5e\x1d\xe1\xc1\x1e\x61\x03\xcb\xf4\xad\xd9\xde\x18\x33\x83\xf6\xe5\xbd\x7d\x01\x15\x84\xae\x3d\xc3\xa1\x5c\x18\xf5\x04\xa8\xe7\xd7\x7d\x5c\xe8\x5c\x60\x6c\x7a\xef\x39\xc8\x65\xa5\xb9\x5e\x53\x4f\x43\x4b\xc2\x2d\x92\x42\xeb\xfe\x7f\xbd\x0a\x5a\x81\xff\x65\xfd\xb8\x13\x8b\x9a\x49\xe5\xc2\xe4\xb1\xe9\xc3\xab\x3b\xcd\xec\xef\x12\xdf\xf9\x33\x3d\xc2\xb5\xf5\x41\xdd\x94\x5e\x68\x69\xfe\x4d\xb9\x98\xbf\x02\x00\x00\xff\xff\x69\x41\xee\x7c\xfd\x01\x00\x00")

func _201911252319_initUpSqlBytes() ([]byte, error) {
	return bindataRead(
		__201911252319_initUpSql,
		"201911252319_init.up.sql",
	)
}

func _201911252319_initUpSql() (*asset, error) {
	bytes, err := _201911252319_initUpSqlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "201911252319_init.up.sql", size: 509, mode: os.FileMode(420), modTime: time.Unix(1574794364, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

// Asset loads and returns the asset for the given name.
// It returns an error if the asset could not be found or
// could not be loaded.
func Asset(name string) ([]byte, error) {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	if f, ok := _bindata[cannonicalName]; ok {
		a, err := f()
		if err != nil {
			return nil, fmt.Errorf("Asset %s can't read by error: %v", name, err)
		}
		return a.bytes, nil
	}
	return nil, fmt.Errorf("Asset %s not found", name)
}

// MustAsset is like Asset but panics when Asset would return an error.
// It simplifies safe initialization of global variables.
func MustAsset(name string) []byte {
	a, err := Asset(name)
	if err != nil {
		panic("asset: Asset(" + name + "): " + err.Error())
	}

	return a
}

// AssetInfo loads and returns the asset info for the given name.
// It returns an error if the asset could not be found or
// could not be loaded.
func AssetInfo(name string) (os.FileInfo, error) {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	if f, ok := _bindata[cannonicalName]; ok {
		a, err := f()
		if err != nil {
			return nil, fmt.Errorf("AssetInfo %s can't read by error: %v", name, err)
		}
		return a.info, nil
	}
	return nil, fmt.Errorf("AssetInfo %s not found", name)
}

// AssetNames returns the names of the assets.
func AssetNames() []string {
	names := make([]string, 0, len(_bindata))
	for name := range _bindata {
		names = append(names, name)
	}
	return names
}

// _bindata is a table, holding each asset generator, mapped to its name.
var _bindata = map[string]func() (*asset, error){
	"201911252319_init.down.sql": _201911252319_initDownSql,
	"201911252319_init.up.sql":   _201911252319_initUpSql,
}

// AssetDir returns the file names below a certain
// directory embedded in the file by go-bindata.
// For example if you run go-bindata on data/... and data contains the
// following hierarchy:
//     data/
//       foo.txt
//       img/
//         a.png
//         b.png
// then AssetDir("data") would return []string{"foo.txt", "img"}
// AssetDir("data/img") would return []string{"a.png", "b.png"}
// AssetDir("foo.txt") and AssetDir("notexist") would return an error
// AssetDir("") will return []string{"data"}.
func AssetDir(name string) ([]string, error) {
	node := _bintree
	if len(name) != 0 {
		cannonicalName := strings.Replace(name, "\\", "/", -1)
		pathList := strings.Split(cannonicalName, "/")
		for _, p := range pathList {
			node = node.Children[p]
			if node == nil {
				return nil, fmt.Errorf("Asset %s not found", name)
			}
		}
	}
	if node.Func != nil {
		return nil, fmt.Errorf("Asset %s not found", name)
	}
	rv := make([]string, 0, len(node.Children))
	for childName := range node.Children {
		rv = append(rv, childName)
	}
	return rv, nil
}

type bintree struct {
	Func     func() (*asset, error)
	Children map[string]*bintree
}

var _bintree = &bintree{nil, map[string]*bintree{
	"201911252319_init.down.sql": &bintree{_201911252319_initDownSql, map[string]*bintree{}},
	"201911252319_init.up.sql":   &bintree{_201911252319_initUpSql, map[string]*bintree{}},
}}

// RestoreAsset restores an asset under the given directory
func RestoreAsset(dir, name string) error {
	data, err := Asset(name)
	if err != nil {
		return err
	}
	info, err := AssetInfo(name)
	if err != nil {
		return err
	}
	err = os.MkdirAll(_filePath(dir, filepath.Dir(name)), os.FileMode(0755))
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(_filePath(dir, name), data, info.Mode())
	if err != nil {
		return err
	}
	err = os.Chtimes(_filePath(dir, name), info.ModTime(), info.ModTime())
	if err != nil {
		return err
	}
	return nil
}

// RestoreAssets restores an asset under the given directory recursively
func RestoreAssets(dir, name string) error {
	children, err := AssetDir(name)
	// File
	if err != nil {
		return RestoreAsset(dir, name)
	}
	// Dir
	for _, child := range children {
		err = RestoreAssets(dir, filepath.Join(name, child))
		if err != nil {
			return err
		}
	}
	return nil
}

func _filePath(dir, name string) string {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	return filepath.Join(append([]string{dir}, strings.Split(cannonicalName, "/")...)...)
}
