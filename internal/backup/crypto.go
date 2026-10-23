package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"

	"github.com/jxburros/GWatch/internal/auth"
)

// Encrypted archive format (all integers big-endian):
//
//	format 1: magic "GWBK" | version u8 (1) | salt [16]byte
//	format 2: magic "GWBK" | version u8 (2) | kdf u8 | salt [16]byte
//	then, either way, repeated:
//	  length u32 | nonce [12]byte | AES-256-GCM ciphertext (length bytes, includes tag)
//
// The key is derived from the password and salt by a key-derivation function.
// Format 1 has no field for it because it only ever used Argon2id; format 2
// (#49) names the function in the byte after the version, so restore knows how
// to turn the password back into the key whichever one the backing-up install
// had selected. Each identifier stands for one function with fixed parameters
// (see deriveKey); a change of parameters would be a new identifier, never a
// reinterpretation of an old one, so an archive always decrypts the way it was
// written.
//
// Argon2id archives are still written as format 1, byte for byte as before.
// That is deliberate: a format-1 file can be restored by every GWatch release,
// including ones older than this change, so somebody who never touches the new
// setting loses nothing — not even the ability to restore onto an older
// install. Only a PBKDF2 archive, which an older release could not decrypt
// anyway, is written as format 2. The reader accepts format 2 with either
// identifier.
//
// Each chunk's nonce embeds a counter so chunks cannot be reordered or
// truncated silently: the final chunk is marked by a nonce whose first byte
// is 0xFF.
var magic = []byte("GWBK")

const (
	formatV1  = 1
	formatV2  = 2
	chunkSize = 1 << 20
	saltSize  = 16
)

// KDF identifies how an archive's key is derived from its password. The values
// are what format 2 writes into the header, so they are fixed forever.
type KDF byte

const (
	// KDFArgon2id is Argon2id with t=3, 64 MiB, p=2 — the derivation every
	// archive used before #49, and still the default.
	KDFArgon2id KDF = 1
	// KDFPBKDF2 is PBKDF2-HMAC-SHA256 from the standard library with
	// pbkdf2Iterations rounds. It is weaker than Argon2id against an attacker
	// with a GPU and a copy of the file, at equal cost to us, because it needs
	// no memory; it is offered only for owners who opt in to depending on the
	// standard library alone (Settings › Users & access).
	KDFPBKDF2 KDF = 2
)

// pbkdf2Iterations is the work factor of KDFPBKDF2. It is part of what the
// identifier means: raising it later needs a new KDF value, or archives
// already written would stop decrypting.
const pbkdf2Iterations = 600_000

// KDFForSetting maps the password-hashing setting
// (GeneralSettings.PasswordHashing) to the derivation used for backups, so one
// switch decides both. Anything unknown, including the empty value older
// settings documents hold, means the Argon2id default.
func KDFForSetting(setting string) KDF {
	if alg, ok := auth.ParseAlgorithm(setting); ok && alg == auth.PBKDF2 {
		return KDFPBKDF2
	}
	return KDFArgon2id
}

func (k KDF) String() string {
	switch k {
	case KDFArgon2id:
		return "argon2id"
	case KDFPBKDF2:
		return "pbkdf2-sha256"
	}
	return fmt.Sprintf("unknown (%d)", byte(k))
}

func deriveKey(kdf KDF, password string, salt []byte) ([]byte, error) {
	switch kdf {
	case KDFArgon2id:
		return argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32), nil
	case KDFPBKDF2:
		return pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	}
	return nil, fmt.Errorf("unsupported backup key derivation %s", kdf)
}

func newGCM(kdf KDF, password string, salt []byte) (cipher.AEAD, error) {
	key, err := deriveKey(kdf, password, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

type encryptWriter struct {
	w       io.Writer
	gcm     cipher.AEAD
	buf     []byte
	counter uint64
	closed  bool
}

// NewEncryptWriter wraps w with password-based encryption using the default
// Argon2id derivation. Close must be called to flush the final chunk.
func NewEncryptWriter(w io.Writer, password string) (io.WriteCloser, error) {
	return NewEncryptWriterKDF(w, password, KDFArgon2id)
}

// NewEncryptWriterKDF is NewEncryptWriter with a chosen key derivation. An
// Argon2id archive gets a format-1 header and a PBKDF2 one a format-2 header
// naming the derivation; see the format description above for why.
func NewEncryptWriterKDF(w io.Writer, password string, kdf KDF) (io.WriteCloser, error) {
	if password == "" {
		return nil, errors.New("a password is required to encrypt the backup")
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	gcm, err := newGCM(kdf, password, salt)
	if err != nil {
		return nil, err
	}
	header := append([]byte{}, magic...)
	if kdf == KDFArgon2id {
		header = append(header, formatV1)
	} else {
		header = append(header, formatV2, byte(kdf))
	}
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

// NewDecryptReader reads an archive produced by NewEncryptWriter or
// NewEncryptWriterKDF, in either header format.
func NewDecryptReader(r io.Reader, password string) (io.Reader, error) {
	kdf, salt, err := readHeader(r)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(kdf, password, salt)
	if err != nil {
		return nil, err
	}
	return &decryptReader{r: r, gcm: gcm}, nil
}

// readHeader consumes the archive header and returns the derivation and salt
// it names. A format-1 header implies Argon2id.
func readHeader(r io.Reader) (KDF, []byte, error) {
	head := make([]byte, len(magic)+1)
	if _, err := io.ReadFull(r, head); err != nil || string(head[:len(magic)]) != string(magic) {
		return 0, nil, fmt.Errorf("not a GWatch backup file")
	}
	kdf := KDFArgon2id
	switch v := head[len(magic)]; v {
	case formatV1:
	case formatV2:
		var b [1]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, fmt.Errorf("not a GWatch backup file")
		}
		kdf = KDF(b[0])
		if kdf != KDFArgon2id && kdf != KDFPBKDF2 {
			return 0, nil, fmt.Errorf("unsupported backup key derivation %s; this backup was made by a newer GWatch", kdf)
		}
	default:
		return 0, nil, fmt.Errorf("unsupported backup format version %d", v)
	}
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(r, salt); err != nil {
		return 0, nil, fmt.Errorf("not a GWatch backup file")
	}
	return kdf, salt, nil
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
