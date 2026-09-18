package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// Encrypted archive format (all integers big-endian):
//
//	magic "GWBK" | version u8 (1) | salt [16]byte
//	repeated: length u32 | nonce [12]byte | AES-256-GCM ciphertext (length bytes, includes tag)
//
// The key is derived with Argon2id from the password and salt. Each chunk's
// nonce embeds a counter so chunks cannot be reordered or truncated silently:
// the final chunk is marked by a nonce whose first byte is 0xFF.
var magic = []byte("GWBK")

const (
	formatVersion = 1
	chunkSize     = 1 << 20
	saltSize      = 16
)

func deriveKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
}

type encryptWriter struct {
	w       io.Writer
	gcm     cipher.AEAD
	buf     []byte
	counter uint64
	closed  bool
}

// NewEncryptWriter wraps w with password-based encryption. Close must be
// called to flush the final chunk.
func NewEncryptWriter(w io.Writer, password string) (io.WriteCloser, error) {
	if password == "" {
		return nil, errors.New("a password is required to encrypt the backup")
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(deriveKey(password, salt))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	header := append(append([]byte{}, magic...), formatVersion)
	header = append(header, salt...)
	if _, err := w.Write(header); err != nil {
		return nil, err
	}
	return &encryptWriter{w: w, gcm: gcm, buf: make([]byte, 0, chunkSize)}, nil
}

func (e *encryptWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		n := chunkSize - len(e.buf)
		if n > len(p) {
			n = len(p)
		}
		e.buf = append(e.buf, p[:n]...)
		p = p[n:]
		if len(e.buf) == chunkSize {
			if err := e.flush(false); err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}

func (e *encryptWriter) flush(final bool) error {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], e.counter)
	if final {
		nonce[0] = 0xFF
	}
	e.counter++
	ct := e.gcm.Seal(nil, nonce, e.buf, nonce)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(ct)))
	if _, err := e.w.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := e.w.Write(nonce); err != nil {
		return err
	}
	if _, err := e.w.Write(ct); err != nil {
		return err
	}
	e.buf = e.buf[:0]
	return nil
}

func (e *encryptWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.flush(true)
}

type decryptReader struct {
	r       io.Reader
	gcm     cipher.AEAD
	buf     []byte
	counter uint64
	done    bool
}

// ErrBadPassword is returned when the archive cannot be decrypted.
var ErrBadPassword = errors.New("wrong password or corrupted backup file")

// NewDecryptReader reads an archive produced by NewEncryptWriter.
func NewDecryptReader(r io.Reader, password string) (io.Reader, error) {
	header := make([]byte, len(magic)+1+saltSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("not a GWatch backup file")
	}
	if string(header[:len(magic)]) != string(magic) {
		return nil, fmt.Errorf("not a GWatch backup file")
	}
	if header[len(magic)] != formatVersion {
		return nil, fmt.Errorf("unsupported backup format version %d", header[len(magic)])
	}
	salt := header[len(magic)+1:]
	block, err := aes.NewCipher(deriveKey(password, salt))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &decryptReader{r: r, gcm: gcm}, nil
}

func (d *decryptReader) Read(p []byte) (int, error) {
	for len(d.buf) == 0 {
		if d.done {
			return 0, io.EOF
		}
		if err := d.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}

func (d *decryptReader) next() error {
	var lenBuf [4]byte
	if _, err := io.ReadFull(d.r, lenBuf[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("backup file is truncated")
		}
		return err
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length > chunkSize+64 {
		return ErrBadPassword
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(d.r, nonce); err != nil {
		return fmt.Errorf("backup file is truncated")
	}
	if binary.BigEndian.Uint64(nonce[4:]) != d.counter {
		return ErrBadPassword
	}
	d.counter++
	ct := make([]byte, length)
	if _, err := io.ReadFull(d.r, ct); err != nil {
		return fmt.Errorf("backup file is truncated")
	}
	pt, err := d.gcm.Open(nil, nonce, ct, nonce)
	if err != nil {
		return ErrBadPassword
	}
	d.buf = pt
	if nonce[0] == 0xFF {
		d.done = true
	}
	return nil
}
