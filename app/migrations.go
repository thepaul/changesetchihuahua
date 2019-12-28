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

var __201911252319_initDownSql = []byte("\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\xff\x72\x09\xf2\x0f\x50\xf0\xf4\x73\x71\x8d\x50\xc8\x49\x2c\x2e\x89\x2f\x4a\x2d\xc8\x2f\x2a\x89\xcf\x4c\xa9\xb0\xe6\x02\xcb\x85\x38\x3a\xf9\xb8\x2a\xa4\xa7\x16\x15\x65\x96\xc4\x97\x16\xa7\x16\x15\x5b\x73\x01\x02\x00\x00\xff\xff\xcc\x39\x77\xa8\x35\x00\x00\x00")

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

	info := bindataFileInfo{name: "201911252319_init.down.sql", size: 53, mode: os.FileMode(420), modTime: time.Unix(1574794364, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var __201911252319_initUpSql = []byte("\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\xff\x64\x8e\xb1\x0a\xc2\x30\x14\x45\xe7\xbe\xaf\xb8\x63\x0b\xfd\x83\x4e\x55\x33\x14\x6b\x2a\x25\x82\x9d\x42\xb0\x0f\x0d\x18\x2d\xc9\x13\xfa\xf9\x2e\x0e\xa1\x8e\xf7\x72\x38\x9c\xfd\xa8\x5a\xa3\x60\xda\x5d\xaf\x70\xe7\x18\xbd\xd8\x4f\xe2\x98\x50\x52\x91\xed\x97\x0b\x0c\xe1\x55\xa0\x07\x03\x7d\xe9\xfb\x9a\x8a\xdb\xc3\x89\xf5\xf3\xdf\xff\x74\x49\x6c\xe4\xe5\x1d\x05\xe2\x03\x27\x71\x61\xa9\xa9\x38\x8f\xdd\xa9\x1d\x27\x1c\xd5\x84\x12\x5b\x7b\x45\x55\x43\xf4\x0b\xea\xf4\x41\x5d\x91\x89\xac\x9f\x57\x0c\x7a\xd3\x98\x13\xa8\x1a\xfa\x06\x00\x00\xff\xff\xdb\x6f\x5e\x67\xcf\x00\x00\x00")

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

	info := bindataFileInfo{name: "201911252319_init.up.sql", size: 207, mode: os.FileMode(420), modTime: time.Unix(1574794364, 0)}
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
