package httpapi

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	stddraw "image/draw"
	_ "image/gif" // decodificadores registrados para image.Decode
	"image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	avatarSide      = 256      // lado da foto guardada (px)
	maxAvatarPixels = 50 << 20 // recusa imagens gigantes antes de decodificar (bomba de descompressão)
)

// normalizeAvatar decodifica a foto enviada, recorta o quadrado central, reduz para 256×256 e devolve um JPEG leve.
// Transparência vira fundo branco. Metadados (EXIF, localização) não passam para a imagem final.
func normalizeAvatar(b []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, errors.New("não foi possível ler a imagem (use PNG, JPEG, GIF ou WebP)")
	}
	if cfg.Width < 16 || cfg.Height < 16 || cfg.Width*cfg.Height > maxAvatarPixels {
		return nil, errors.New("a imagem deve ter entre 16 px e cerca de 50 megapixels")
	}
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, errors.New("não foi possível ler a imagem (use PNG, JPEG, GIF ou WebP)")
	}
	r := src.Bounds()
	side := min(r.Dx(), r.Dy())
	crop := image.Rect(r.Min.X+(r.Dx()-side)/2, r.Min.Y+(r.Dy()-side)/2, 0, 0)
	crop.Max = crop.Min.Add(image.Pt(side, side))
	out := min(avatarSide, side)
	dst := image.NewRGBA(image.Rect(0, 0, out, out))
	stddraw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 86}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
