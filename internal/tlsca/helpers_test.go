package tlsca

import "encoding/pem"

func pemBlocks(data []byte) []*pem.Block {
	var out []*pem.Block
	for {
		var b *pem.Block
		b, data = pem.Decode(data)
		if b == nil {
			return out
		}
		out = append(out, b)
	}
}
