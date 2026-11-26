package themepkg

import (
	"fmt"
	"math"
	"strconv"
)

// DeriveDark gera um modo escuro legível a partir das cores claras de um tema: fundos escuros levemente tingidos pela cor
// principal, texto claro e as cores de destaque clareadas para manter o contraste. Usado quando o pacote não traz
// "colors_dark".
func DeriveDark(light map[string]string) map[string]string {
	h, s, _ := toHSL(light["primary"])
	tint := func(l float64) string { return fromHSL(h, math.Min(s, 0.25), l) }
	brighten := func(c string, min float64) string {
		ch, cs, cl := toHSL(c)
		return fromHSL(ch, cs, math.Max(cl, min))
	}
	primary := brighten(light["primary"], 0.62)
	contrast := "#ffffff"
	if luminance(primary) > 0.45 {
		contrast = tint(0.08)
	}
	return map[string]string{
		"bg": tint(0.08), "surface": tint(0.12), "text": "#e9ebf1", "muted": "#a0a6b5", "border": tint(0.22),
		"primary": primary, "primary_contrast": contrast, "accent": brighten(light["accent"], 0.6),
		"ok": brighten(light["ok"], 0.55), "danger": brighten(light["danger"], 0.62),
	}
}

func rgbOf(c string) (r, g, b float64) {
	if len(c) != 7 {
		return 0.5, 0.5, 0.5
	}
	v, err := strconv.ParseUint(c[1:], 16, 32)
	if err != nil {
		return 0.5, 0.5, 0.5
	}
	return float64(v>>16&255) / 255, float64(v>>8&255) / 255, float64(v&255) / 255
}

func luminance(c string) float64 {
	r, g, b := rgbOf(c)
	lin := func(x float64) float64 {
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

func toHSL(c string) (h, s, l float64) {
	r, g, b := rgbOf(c)
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l = (mx + mn) / 2
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case r:
		h = math.Mod((g-b)/d+6, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h / 6, s, l
}

func fromHSL(h, s, l float64) string {
	hue := func(p, q, t float64) float64 {
		t = math.Mod(t+1, 1)
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	var r, g, b float64
	if s == 0 {
		r, g, b = l, l, l
	} else {
		q := l * (1 + s)
		if l >= 0.5 {
			q = l + s - l*s
		}
		p := 2*l - q
		r, g, b = hue(p, q, h+1.0/3), hue(p, q, h), hue(p, q, h-1.0/3)
	}
	return fmt.Sprintf("#%02x%02x%02x", int(math.Round(r*255)), int(math.Round(g*255)), int(math.Round(b*255)))
}
