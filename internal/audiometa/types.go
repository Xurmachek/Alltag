package audiometa

// Field is one tag entry: an upper-case key and its value.
type Field struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Picture is an embedded image.
type Picture struct {
	MIME string `json:"mime"`
	Kind int    `json:"kind"`
	Desc string `json:"desc"`
	Data []byte `json:"-"`
}

// Info carries stream properties.
type Info struct {
	Duration   float64 `json:"duration"`
	Bitrate    int     `json:"bitrate"`
	SampleRate int     `json:"sampleRate"`
	Channels   int     `json:"channels"`
	BitDepth   int     `json:"bitDepth"`
	Codec      string  `json:"codec"`
	Lossless   bool    `json:"lossless"`
}

// Tags is the metadata of a single file.
type Tags struct {
	Path     string
	Name     string
	Format   string
	TagType  string
	Fields   []Field
	Pictures []Picture
	Info     Info
	Warnings []string
}

// Get returns the first value of a key.
func (t *Tags) Get(key string) string {
	key = CanonicalKey(key)
	for _, f := range t.Fields {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}

// Edits describes a batch edit; an empty value removes the field.
type Edits struct {
	Set               map[string]string
	Remove            []string
	Picture           []byte
	PictureMIME       string
	PictureAction     string
	RemoveAllPictures bool
	TagVersion        string
}

// SupportedExt reports whether the extension (with or without a dot) is handled.
func SupportedExt(ext string) bool {
	if ext == "" {
		return false
	}
	if ext[0] != '.' {
		ext = "." + ext
	}
	switch lower(ext) {
	case ".mp3", ".mp2", ".mp1", ".flac", ".ogg", ".oga", ".opus", ".spx",
		".m4a", ".m4b", ".mp4", ".aac", ".alac", ".wma", ".asf", ".ape",
		".wav", ".wave", ".aif", ".aiff", ".aifc", ".wv", ".mpc", ".mp+":
		return true
	}
	return false
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
