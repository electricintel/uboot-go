package uenv

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"os"
	"strings"
)

// FIXME: add config option for that so that the user can select if
//        he/she wants env with or without flags
var headerSize = 5

// Env contains the data of the uboot environment
type Env struct {
	fname string
	size  int
	data  map[string]string
}

// little endian helpers
func readUint32(data []byte) uint32 {
	var ret uint32
	buf := bytes.NewBuffer(data)
	binary.Read(buf, binary.LittleEndian, &ret)
	return ret
}

func writeUint32(u uint32) []byte {
	buf := bytes.NewBuffer(nil)
	binary.Write(buf, binary.LittleEndian, &u)
	return buf.Bytes()
}

// Create a new empty uboot env file with the given size
func Create(fname string, size int) (*Env, error) {
	f, err := os.Create(fname)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	env := &Env{
		fname: fname,
		size:  size,
		data:  make(map[string]string),
	}

	return env, nil
}

// Open opens a existing uboot env file
func Open(fname string) (*Env, error) {
	f, err := os.Open(fname)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	content, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}

	crc := readUint32(content)
	actualCRC := crc32.ChecksumIEEE(content[headerSize:])
	if crc != actualCRC {
		return nil, fmt.Errorf("bad CRC: %v != %v", crc, actualCRC)
	}

	env := &Env{
		fname: fname,
		size:  len(content),
		data:  parseData(content[headerSize:]),
	}

	return env, nil
}

func parseData(data []byte) map[string]string {
	out := make(map[string]string)

	for _, envStr := range bytes.Split(data, []byte{0}) {
		if len(envStr) == 0 || envStr[0] == 0 || envStr[0] == 255 {
			continue
		}
		l := strings.SplitN(string(envStr), "=", 2)
		out[l[0]] = l[1]
	}

	return out
}

func (env *Env) String() string {
	out := ""

	for k, v := range env.data {
		out += fmt.Sprintf("%s=%s\n", k, v)
	}

	return out
}

// Get the value of the environment variable
func (env *Env) Get(name string) string {
	return env.data[name]
}

// Set an environment name to the given value, if the value is empty
// the variable will be removed from the environment
func (env *Env) Set(name, value string) {
	if value == "" {
		delete(env.data, name)
		return
	}
	env.data[name] = value
}

// Save will write out the environment data
func (env *Env) Save() error {
	w := bytes.NewBuffer(nil)
	// will panic if the buffer can't grow, all writes to
	// the buffer will be ok because we sized it correctly
	w.Grow(env.size - headerSize)
	for k, v := range env.data {
		w.Write([]byte(fmt.Sprintf("%s=%s", k, v)))
		w.Write([]byte{0})
	}
	// ensure buffer is exactly the size we need it to be
	w.Write(make([]byte, env.size-headerSize-w.Len()))
	crc := crc32.ChecksumIEEE(w.Bytes())

	// Note that we overwrite the existing file and do not do
	// the usual write-rename. The rationale is that we want to
	// minimize the amount of writes happening on a potential
	// FAT partition where the env is loaded from. The file will
	// always be of a fixed size so we know the writes will not
	// fail because of ENOSPC.
	//
	// The size of the env file never changes so we do not
	// truncate it.
	//
	// We also do not O_TRUNC to avoid reallocations on the FS
	// to minimize risk of fs corruption.
	f, err := os.OpenFile(env.fname, os.O_WRONLY, 0666)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(writeUint32(crc)); err != nil {
		return err
	}
	// padding bytes (e.g. for redundant header)
	pad := make([]byte, headerSize-binary.Size(crc))
	if _, err := f.Write(pad); err != nil {
		return err
	}
	if _, err := f.Write(w.Bytes()); err != nil {
		return err
	}

	return f.Sync()
}

// Import is a helper that imports a given text file that contains
// "key=value" paris into the uboot env. Lines starting with ^# are
// ignored (like the input file on mkenvimage)
func (env *Env) Import(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || len(line) == 0 {
			continue
		}
		l := strings.SplitN(line, "=", 2)
		if len(l) == 1 {
			return fmt.Errorf("Invalid line: %q", line)
		}
		env.data[l[0]] = l[1]

	}

	return scanner.Err()
}
