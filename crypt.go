package pdfdisassembler

import (
	"errors"
	"fmt"

	"github.com/speedata/pdfdisassembler/internal/crypt"
)

// encryptCtx wraps the document-level encryption state. nil if the PDF is
// unencrypted.
type encryptCtx struct {
	handler *crypt.Handler
	// password is retained for re-derivation if needed.
	password []byte
}

// initEncrypt reads the trailer /Encrypt entry, builds the crypt.Handler
// using the empty user password. Documents secured with a non-empty user
// password fail to open via the public API; callers will need an explicit
// password hook (TODO: expose).
func (r *Reader) initEncrypt() error {
	if r.trailer == nil {
		return nil
	}
	encObj, ok := r.trailer.Get("Encrypt")
	if !ok {
		return nil
	}
	encDict, err := r.ResolveDict(encObj)
	if err != nil {
		return fmt.Errorf("pdfdisassembler: /Encrypt: %w", err)
	}

	filter, _ := encDict.Name("Filter")
	if filter != "Standard" {
		return fmt.Errorf("pdfdisassembler: encryption filter %q not supported", filter)
	}

	params, err := encryptParamsFromDict(r, encDict)
	if err != nil {
		return err
	}

	h, err := crypt.New(params, nil)
	if err != nil {
		return fmt.Errorf("pdfdisassembler: encryption: %w", err)
	}
	r.encrypt = &encryptCtx{handler: h}
	return nil
}

func encryptParamsFromDict(r *Reader, d *Dict) (crypt.Params, error) {
	var p crypt.Params
	if v, ok := d.Int("V"); ok {
		p.V = int(v)
	}
	if v, ok := d.Int("R"); ok {
		p.R = int(v)
	}
	if v, ok := d.Int("Length"); ok {
		p.Length = int(v)
	} else {
		p.Length = 40 // V1 default
	}
	if v, ok := d.Int("P"); ok {
		p.P = int32(v)
	}
	if o, ok := d.Bytes("O"); ok {
		p.OwnerEntry = o
	}
	if u, ok := d.Bytes("U"); ok {
		p.UserEntry = u
	}
	if oe, ok := d.Bytes("OE"); ok {
		p.OE = oe
	}
	if ue, ok := d.Bytes("UE"); ok {
		p.UE = ue
	}
	if perms, ok := d.Bytes("Perms"); ok {
		p.Perms = perms
	}
	p.EncryptMeta = true
	if v, ok := d.Bool("EncryptMetadata"); ok {
		p.EncryptMeta = v
	}
	if n, ok := d.Name("StmF"); ok {
		p.StmF = string(n)
	}
	if n, ok := d.Name("StrF"); ok {
		p.StrF = string(n)
	}
	if n, ok := d.Name("EFF"); ok {
		p.EFF = string(n)
	}
	// File ID first element comes from trailer.
	if id, ok := r.trailer.Array("ID"); ok && len(id) >= 1 {
		if s, ok := id[0].(String); ok {
			p.ID0 = []byte(s)
		}
	}

	// /CF dictionary: name → CFM string.
	p.CryptFilters = map[string]string{}
	if cf, ok := d.Dict("CF"); ok {
		for name, v := range cf.Iter() {
			cd, ok := v.(*Dict)
			if !ok {
				continue
			}
			if cfm, ok := cd.Name("CFM"); ok {
				p.CryptFilters[name] = string(cfm)
			}
		}
	}
	return p, nil
}

func (e *encryptCtx) decryptStream(data []byte, objNum, objGen int, dict *Dict) ([]byte, error) {
	// Per-stream /Filter chain may contain /Crypt with a parameter dict;
	// for now we use the default stream cipher.
	return e.handler.DecryptStream(data, objNum, objGen, "")
}

func (e *encryptCtx) decryptString(data []byte, objNum, objGen int) ([]byte, error) {
	return e.handler.DecryptString(data, objNum, objGen)
}

// guard against accidental nil deref in callers
var _ = errors.New
