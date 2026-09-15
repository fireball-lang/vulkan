package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Registry struct {
	Types      []Type      `xml:"types>type"`
	Enums      []Enum      `xml:"enums"`
	Commands   []Command   `xml:"commands>command"`
	Features   []Feature   `xml:"feature"`
	Extensions []Extension `xml:"extensions>extension"`
}

type Type struct {
	Category string `xml:"category,attr"`

	AttrName string `xml:"name,attr"`
	NameElem string `xml:"name"`
	Alias    string `xml:"alias,attr"`

	TypeElem string `xml:"type"`

	Parent        string `xml:"parent,attr"`
	Requires      string `xml:"requires,attr"`
	BitValues     string `xml:"bitvalues,attr"`
	StructExtends string `xml:"structextends,attr"`

	Proto  Prototype  `xml:"proto"`
	Params []NameType `xml:"param"`

	Fields []NameType `xml:"member"`
}

type NameType struct {
	Name string
	Type string

	Values   string
	Len      string
	Optional bool
}

func (f *NameType) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "values":
			f.Values = attr.Value
		case "len":
			f.Len = attr.Value
		case "optional":
			f.Optional = attr.Value == "true" || strings.HasPrefix(attr.Value, "true,")
		}
	}

	return unmarshalNameType(d, start, &f.Name, &f.Type)
}

type Enum struct {
	Name     string `xml:"name,attr"`
	Type     string `xml:"type,attr"`
	BitWidth int    `xml:"bitwidth,attr"`

	Cases []Case `xml:"enum"`
}

type Case struct {
	Name  string `xml:"name,attr"`
	Alias string `xml:"alias,attr"`

	Value  string `xml:"value,attr"`
	BitPos *int   `xml:"bitpos,attr"`
}

type Command struct {
	Name  string `xml:"name,attr"`
	Alias string `xml:"alias,attr"`

	Proto  Prototype  `xml:"proto"`
	Params []NameType `xml:"param"`
}

type Prototype struct {
	Name    string
	Returns string
}

func (p *Prototype) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	return unmarshalNameType(d, start, &p.Name, &p.Returns)
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

func unmarshalNameType(d *xml.Decoder, start xml.StartElement, name, typ *string) error {
	var preName strings.Builder
	var postName strings.Builder
	foundName := false

	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "name":
				var str string
				if err := d.DecodeElement(&str, &t); err != nil {
					return err
				}
				*name = strings.TrimSpace(str)
				foundName = true

			case "type":
				var str string
				if err := d.DecodeElement(&str, &t); err != nil {
					return err
				}
				preName.WriteString(str)

			case "enum":
				var str string
				if err := d.DecodeElement(&str, &t); err != nil {
					return err
				}

				if foundName {
					postName.WriteString(str)
				} else {
					preName.WriteString(str)
				}

			case "comment":
				if err := d.Skip(); err != nil {
					return err
				}

			default:
				if err := d.Skip(); err != nil {
					return err
				}
			}

		case xml.CharData:
			if !foundName {
				preName.Write(t)
			} else {
				postName.Write(t)
			}

		case xml.EndElement:
			if t == start.End() {
				base := strings.Join(strings.Fields(preName.String()), " ")
				suffix := strings.Join(strings.Fields(postName.String()), "")

				*typ = base + suffix
				return nil
			}
		}
	}
}
