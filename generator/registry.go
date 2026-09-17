package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

type Registry struct {
	Types      []Type      `xml:"types>type"`
	Enums      []Enum      `xml:"enums"`
	Commands   []Command   `xml:"commands>command"`
	Features   []Feature   `xml:"feature"`
	Extensions []Extension `xml:"extensions>extension"`
	Tags       []Tag       `xml:"tags>tag"`
}

type Tag struct {
	Name string `xml:"name,attr"`
}

type Type struct {
	Category string      `xml:"category,attr"`
	Api      StringSlice `xml:"api,attr"`

	AttrName string `xml:"name,attr"`
	NameElem string `xml:"name"`
	Alias    string `xml:"alias,attr"`

	TypeElem string `xml:"type"`
	Comment  string `xml:"comment,attr"`

	Parent        string `xml:"parent,attr"`
	Requires      string `xml:"requires,attr"`
	BitValues     string `xml:"bitvalues,attr"`
	StructExtends string `xml:"structextends,attr"`

	Proto  Prototype  `xml:"proto"`
	Params []NameType `xml:"param"`

	Fields []Member `xml:"member"`
}

type typeParts struct {
	Base    string
	Const   bool
	Pointer int
	Dims    []string
	Bits    int
}

type Member struct {
	Name string

	typeParts

	Api      StringSlice
	Optional bool
	Values   StringSlice
}

func (m *Member) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "api":
			_ = m.Api.UnmarshalText([]byte(attr.Value))
		case "values":
			_ = m.Values.UnmarshalText([]byte(attr.Value))
		case "optional":
			m.Optional = attr.Value == "true" || strings.HasPrefix(attr.Value, "true,")
		}
	}

	return unmarshalTyped(d, start, &m.typeParts, &m.Name)
}

func unmarshalTyped(d *xml.Decoder, start xml.StartElement, parts *typeParts, name *string) error {
	dim := strings.Builder{}
	inDim := false

	flushDim := func() {
		if str := strings.TrimSpace(dim.String()); str != "" {
			parts.Dims = append(parts.Dims, str)
		}

		dim.Reset()
	}

	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "type":
				var str string
				if err := d.DecodeElement(&str, &t); err != nil {
					return err
				}

				if parts.Base == "" {
					parts.Base = strings.TrimSpace(str)
				}

			case "name":
				var str string
				if err := d.DecodeElement(&str, &t); err != nil {
					return err
				}

				*name = strings.TrimSpace(str)

			case "enum":
				var str string
				if err := d.DecodeElement(&str, &t); err != nil {
					return err
				}

				parts.Dims = append(parts.Dims, str)

			default:
				if err := d.Skip(); err != nil {
					return err
				}
			}

		case xml.CharData:
			text := string(t)

			for _, r := range text {
				switch r {
				case '[':
					inDim = true
				case ']':
					inDim = false
					flushDim()
				default:
					if r == '*' && !inDim {
						parts.Pointer++
					} else if inDim {
						dim.WriteRune(r)
					}
				}
			}

			if !inDim && slices.Contains(strings.Fields(text), "const") {
				parts.Const = true
			}

			if !inDim {
				if _, after, ok := strings.Cut(text, ":"); ok {
					if width, err := strconv.ParseUint(strings.TrimSpace(after), 10, 32); err == nil {
						parts.Bits = int(width)
					}
				}
			}

		case xml.EndElement:
			if t == start.End() {
				flushDim()
				return nil
			}
		}
	}
}

type NameType struct {
	Name string

	typeParts

	Api      StringSlice
	Values   StringSlice
	Len      string
	Optional bool
}

func (f *NameType) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "api":
			_ = f.Api.UnmarshalText([]byte(attr.Value))
		case "values":
			_ = f.Values.UnmarshalText([]byte(attr.Value))
		case "len":
			f.Len = attr.Value
		case "optional":
			f.Optional = attr.Value == "true" || strings.HasPrefix(attr.Value, "true,")
		}
	}

	return unmarshalTyped(d, start, &f.typeParts, &f.Name)
}

type Enum struct {
	Name     string `xml:"name,attr"`
	Type     string `xml:"type,attr"`
	BitWidth int    `xml:"bitwidth,attr"`
	Comment  string `xml:"comment,attr"`

	Cases []Case `xml:"enum"`
}

type Case struct {
	Name    string `xml:"name,attr"`
	Alias   string `xml:"alias,attr"`
	Comment string `xml:"comment,attr"`
	Type    string `xml:"type,attr"`

	Value  string `xml:"value,attr"`
	BitPos *int   `xml:"bitpos,attr"`
}

type Command struct {
	Name  string      `xml:"name,attr"`
	Alias string      `xml:"alias,attr"`
	Api   StringSlice `xml:"api,attr"`

	Proto  Prototype  `xml:"proto"`
	Params []NameType `xml:"param"`
}

type Prototype struct {
	Name string

	Returns typeParts
}

func (p *Prototype) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	return unmarshalTyped(d, start, &p.Returns, &p.Name)
}

type Feature struct {
	Apis    StringSlice `xml:"api,attr"`
	ApiType string      `xml:"apitype,attr"`
	Name    string      `xml:"name,attr"`
	Version Version     `xml:"number,attr"`

	Requires   []RefList `xml:"require"`
	Deprecates []RefList `xml:"deprecate"`
	Removes    []RefList `xml:"remove"`
}

type Extension struct {
	Name      string      `xml:"name,attr"`
	Number    string      `xml:"number,attr"`
	Type      string      `xml:"type,attr"`
	Author    string      `xml:"author,attr"`
	Supported StringSlice `xml:"supported,attr"`
	Ratified  StringSlice `xml:"ratified,attr"`

	Requires []RefList `xml:"require"`
}

type RefList struct {
	Api StringSlice `xml:"api,attr"`

	Types []struct {
		Name string `xml:"name,attr"`
	} `xml:"type"`

	Commands []struct {
		Name string `xml:"name,attr"`
	} `xml:"command"`

	Enums []ExtensionEnum `xml:"enum"`
}

type ExtensionEnum struct {
	Name      string `xml:"name,attr"`
	Extends   string `xml:"extends,attr"`
	Type      string `xml:"type,attr"`
	Value     string `xml:"value,attr"`
	BitPos    *int   `xml:"bitpos,attr"`
	ExtNumber *int   `xml:"extnumber,attr"`
	Offset    *int   `xml:"offset,attr"`
	Dir       string `xml:"dir,attr"`
	Alias     string `xml:"alias,attr"`
}

type StringSlice []string

func (s *StringSlice) UnmarshalText(text []byte) error {
	str := strings.TrimSpace(string(text))
	if str == "" {
		*s = nil
		return nil
	}

	*s = strings.Split(str, ",")
	return nil
}

type Version struct {
	Major int
	Minor int
}

func (v *Version) UnmarshalText(text []byte) error {
	parts := strings.Split(string(text), ".")
	if len(parts) != 2 {
		return fmt.Errorf("invalid version format %q (expected X.Y)", string(text))
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("invalid major version: %w", err)
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("invalid minor version: %w", err)
	}

	v.Major = major
	v.Minor = minor

	return nil
}

func LoadRegistry() (Registry, error) {
	file, err := os.Open("vk.xml")
	if err != nil {
		return Registry{}, err
	}

	//goland:noinspection GoUnhandledErrorResult
	defer file.Close()

	var reg Registry
	if err := xml.NewDecoder(file).Decode(&reg); err != nil {
		return Registry{}, err
	}

	return reg, nil
}
