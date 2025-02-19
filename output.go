package generate

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
)

func getOrderedFieldNames(m map[string]Field) []string {
	keys := make([]string, len(m))
	idx := 0
	for k := range m {
		keys[idx] = k
		idx++
	}
	sort.Strings(keys)
	return keys
}

func getOrderedStructNames(m map[string]Struct) []string {
	keys := make([]string, len(m))
	idx := 0
	for k := range m {
		keys[idx] = k
		idx++
	}
	sort.Strings(keys)
	return keys
}

// returns the stringified value to check against if possible. For structs (without pointers)
// you can't check the zero value without using the reflect package
func getZeroValueCheck(schemaType string) (string, bool) {
	if strings.HasPrefix(schemaType, "*") {
		return "nil", true
	}
	if strings.HasPrefix(schemaType, "[]") {
		return "nil", true
	}
	switch schemaType {
	case "array":
		return "nil", true
	case "bool":
		return "false", true
	case "int":
		return "0", true
	case "float64":
		return "0", true
	case "nil":
		return "nil", true
	case "string":
		return `""`, true
	}
	return "", false
}

// Output generates code and writes to w.
func Output(w io.Writer, g *Generator, pkg string) {
	structs := g.Structs
	aliases := g.Aliases

	fmt.Fprintln(w, "// Code generated by schema-generate. DO NOT EDIT.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "package %v\n", cleanPackageName(pkg))

	// write all the code into a buffer, compiler functions will return list of imports
	// write list of imports into main output stream, followed by the code
	codeBuf := new(bytes.Buffer)
	imports := make(map[string]bool)

	for _, k := range getOrderedStructNames(structs) {
		s := structs[k]
		if s.GenerateCode {
			emitMarshalCode(codeBuf, s, imports)
			emitUnmarshalCode(codeBuf, s, imports)
			emitToMapCode(codeBuf, s)
		}
	}

	if len(imports) > 0 {
		fmt.Fprintf(w, "\nimport (\n")
		for k := range imports {
			fmt.Fprintf(w, "    \"%s\"\n", k)
		}
		fmt.Fprintf(w, ")\n")
	}

	for _, k := range getOrderedFieldNames(aliases) {
		a := aliases[k]

		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "// %s\n", a.Name)
		fmt.Fprintf(w, "type %s %s\n", a.Name, a.UnmarshalType)
	}

	for _, k := range getOrderedStructNames(structs) {
		s := structs[k]

		fmt.Fprintln(w, "")
		outputNameAndDescriptionComment(s.Name, s.Description, w)
		fmt.Fprintf(w, "type %s struct {\n", s.Name)

		for _, fieldKey := range getOrderedFieldNames(s.Fields) {
			f := s.Fields[fieldKey]

			// Only apply omitempty if the field is not required.
			// omitempty := ",omitempty"
			// if f.Required {
			// 	omitempty = ""
			// }

			if f.Description != "" {
				outputFieldDescriptionComment(f.Description, w)
			}

			// fmt.Fprintf(w, "  %s %s `json:\"%s%s\"`\n", f.Name, f.UnmarshalType, f.UnmarshalName, omitempty)
			fmt.Fprintf(w, "  %s %s\n", f.Name, f.MarshalType)
		}

		fmt.Fprintln(w, "}")
	}

	// write code after structs for clarity
	w.Write(codeBuf.Bytes())
}

func emitMarshalCode(w io.Writer, s Struct, imports map[string]bool) {
	fmt.Fprintf(w,
		`
func (strct %s) MarshalJSON() ([]byte, error) {
	lines := []string{}
	
`, s.Name)

	if len(s.Fields) > 0 {
		// Marshal all the defined fields
		for _, fieldKey := range getOrderedFieldNames(s.Fields) {
			f := s.Fields[fieldKey]
			if f.MarshalName == "-" {
				continue
			}
			if f.Required {
				fmt.Fprintf(w, "    // \"%s\" field is required\n", f.Name)
				// currently only objects are supported
				if strings.HasPrefix(f.MarshalType, "*") {
					imports["errors"] = true
					fmt.Fprintf(w, `    if strct.%s == nil {
        return nil, errors.New("%s is a required field")
    }
`, f.Name, f.MarshalName)
				} else {
					fmt.Fprintf(w, "    // only required object types supported for marshal checking (for now)\n")
				}
			}

			if f.OmitEmpty {
				zeroVal, haveZeroVal := getZeroValueCheck(f.MarshalType)
				if haveZeroVal {
					fmt.Fprintf(w,
						`	// omit empty
	if strct.%s != %s {
`, f.Name, zeroVal)
				} else {
					fmt.Fprintf(w,
						`	// Check using reflect.Value
	if reflect.ValueOf(strct.%s).IsZero() {
`, f.Name)
				}
			}

			fmt.Fprintf(w,
				`  // Marshal the "%[1]s" field
	if tmp, err := json.Marshal(strct.%[2]s); err != nil {
		return nil, err
	} else {
`, f.MarshalName, f.Name)
			imports["fmt"] = true
			fmt.Fprintf(w, `lines = append(lines, fmt.Sprintf("\"%[1]s\": %%s", tmp))`, f.MarshalName)

			if f.OmitEmpty {
				fmt.Fprintf(w, `
	}
}

`)
			} else {
				fmt.Fprintf(w, `
				}

`)
			}
		}
	}
	if s.AdditionalType != "" {
		if s.AdditionalType != "false" {
			imports["fmt"] = true

			fmt.Fprintf(w, "    // Marshal any additional Properties\n")
			// Marshal any additional Properties
			fmt.Fprintf(w, `    for k, v := range strct.AdditionalProperties {`)
			fmt.Fprintf(w, `
			if tmp, err := json.Marshal(v); err != nil {
				return nil, err
			} else {
				lines = append(lines, fmt.Sprintf("\"%%s\": %%s", k, tmp))
			}
	}
`)
		}
	}

	imports["strings"] = true
	fmt.Fprintf(w, `
	return []byte("{" + strings.Join(lines, ", ") + "}"), nil
}
`)
}

func emitUnmarshalFieldCode(w io.Writer, f Field, imports map[string]bool) {
	if f.MarshalType == f.UnmarshalType {
		fmt.Fprintf(w, `        case "%s":
            if err := json.Unmarshal([]byte(v), &strct.%s); err != nil {
                return err
             }
`, f.UnmarshalName, f.Name)

		return
	}

	switch f.UnmarshalType {
	case "string":
		switch f.MarshalType {
		case "int":
			fmt.Fprintf(w, `        case "%s":
            if newVal, err := strconv.ParseInt(v, 10, 0); err != nil {
                return err
             }
            if err := json.Unmarshal([]byte(newVal), &strct.%s); err != nil {
                return err
             }
`, f.UnmarshalName, f.Name)

			return
		default:
			return
		}
	case "int":
		switch f.MarshalType {
		case "string":
			imports["strconv"] = true
			fmt.Fprintf(w, `        case "%s":
			var intVal int
            if err := json.Unmarshal([]byte(v), &intVal); err != nil {
                return err
             }
            strct.%s = strconv.Itoa(intVal)
`, f.UnmarshalName, f.Name)

			return
		default:
			return
		}
	default:
		return
	}
}

func emitUnmarshalCode(w io.Writer, s Struct, imports map[string]bool) {
	imports["encoding/json"] = true
	// unmarshal code
	fmt.Fprintf(w, `
func (strct *%s) UnmarshalJSON(b []byte) error {
`, s.Name)
	// setup required bools
	for _, fieldKey := range getOrderedFieldNames(s.Fields) {
		f := s.Fields[fieldKey]
		if f.Required {
			fmt.Fprintf(w, "    %sReceived := false\n", f.UnmarshalName)
		}
	}
	// setup initial unmarshal
	fmt.Fprintf(w, `    var jsonMap map[string]json.RawMessage
    if err := json.Unmarshal(b, &jsonMap); err != nil {
        return err
    }`)

	// start the loop
	fmt.Fprintf(w, `
    // parse all the defined properties
    for k, v := range jsonMap {
        if v != nil {
			switch k {
`)
	// handle defined properties
	for _, fieldKey := range getOrderedFieldNames(s.Fields) {
		f := s.Fields[fieldKey]

		if f.UnmarshalName == "-" {
			continue
		}

		emitUnmarshalFieldCode(w, f, imports)

		if f.Required {
			fmt.Fprintf(w, "            %sReceived = true\n", f.UnmarshalName)
		}
	}

	// handle additional property
	if s.AdditionalType != "" {
		if s.AdditionalType == "false" {
			// all unknown properties are not allowed
			imports["fmt"] = true
			fmt.Fprintf(w, `        default:
            continue
`)
		} else {
			fmt.Fprintf(w, `        default:
            // an additional "%s" value
            var additionalValue %s
            if err := json.Unmarshal([]byte(v), &additionalValue); err != nil {
                return err // invalid additionalProperty
            }
            if strct.AdditionalProperties == nil {
                strct.AdditionalProperties = make(map[string]%s, 0)
            }
            strct.AdditionalProperties[k]= additionalValue
`, s.AdditionalType, s.AdditionalType, s.AdditionalType)
		}
	}
	fmt.Fprintf(w, "        }}\n") // switch
	fmt.Fprintf(w, "    }\n")      // for

	// check all Required fields were received
	for _, fieldKey := range getOrderedFieldNames(s.Fields) {
		f := s.Fields[fieldKey]
		if f.Required {
			imports["errors"] = true
			fmt.Fprintf(w, `    // check if %s (a required property) was received
    if !%sReceived {
        return errors.New("\"%s\" is required but was not present")
    }
`, f.UnmarshalName, f.UnmarshalName, f.UnmarshalName)
		}
	}

	fmt.Fprintf(w, "    return nil\n")
	fmt.Fprintf(w, "}\n") // UnmarshalJSON
}

func emitToMapCode(w io.Writer, s Struct) {
	// ToMap code
	fmt.Fprintf(w, `
func (strct *%s) ToMap() map[string]any {
`, s.Name)

	fmt.Fprintf(w, "    m := make(map[string]any)\n")

	for _, fieldKey := range getOrderedFieldNames(s.Fields) {
		f := s.Fields[fieldKey]
		fmt.Fprintf(w, "    m[\"%s\"] = strct.%s\n", f.MarshalName, f.Name)
	}

	fmt.Fprintf(w, "    return m\n")
	fmt.Fprintf(w, "}\n") // ToMap
}

func outputNameAndDescriptionComment(name, description string, w io.Writer) {
	if strings.Index(description, "\n") == -1 {
		fmt.Fprintf(w, "// %s %s\n", name, description)
		return
	}

	dl := strings.Split(description, "\n")
	fmt.Fprintf(w, "// %s %s\n", name, strings.Join(dl, "\n// "))
}

func outputFieldDescriptionComment(description string, w io.Writer) {
	if strings.Index(description, "\n") == -1 {
		fmt.Fprintf(w, "\n  // %s\n", description)
		return
	}

	dl := strings.Split(description, "\n")
	fmt.Fprintf(w, "\n  // %s\n", strings.Join(dl, "\n  // "))
}

func cleanPackageName(pkg string) string {
	pkg = strings.Replace(pkg, ".", "", -1)
	pkg = strings.Replace(pkg, "-", "", -1)
	return pkg
}
