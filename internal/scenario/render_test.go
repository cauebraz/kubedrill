package scenario

import (
	"strings"
	"testing"
	"time"
)

func TestExecuteBoundsOutput(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		seed    int64
		want    string
		wantErr string
	}{
		{name: "small template renders", text: "{{range 3}}x{{end}}", want: "xxx"},
		{name: "large literal range is rejected", text: "{{range 10000000000}}x{{end}}", wantErr: "range over 10000000000 exceeds 100000 iterations"},
		{name: "literal range beyond int64 is rejected", text: "{{range 18446744073709551615}}{{end}}", wantErr: "exceeds 100000 iterations"},
		{name: "large range in a define is rejected", text: `{{define "t"}}{{range 200000}}{{end}}{{end}}ok`, wantErr: "range over 200000 exceeds"},
		{name: "large range in an else branch is rejected", text: "{{if false}}{{else}}{{range 200000}}{{end}}{{end}}", wantErr: "range over 200000 exceeds"},
		{name: "output past the cap fails", text: "{{range 50000}}" + strings.Repeat("y", 100) + "{{end}}", wantErr: "f.yaml: rendered output exceeds 4194304 bytes"},
		{name: "range over a large seed fails at the step budget", text: "{{range .Seed}}x{{end}}", seed: 10000000000, wantErr: "f.yaml: template runs more than 1000000 steps"},
		{name: "silent range over a large seed fails at the step budget", text: "{{range .Seed}}{{end}}", seed: 10000000000, wantErr: "template runs more than 1000000 steps"},
		{name: "silent range over a variable fails at the step budget", text: "{{$n := .Seed}}{{range $i := $n}}{{end}}", seed: 10000000000, wantErr: "template runs more than 1000000 steps"},
		{name: "nested silent literal ranges fail at the step budget", text: "{{range 100000}}{{range 100000}}{{end}}{{end}}", wantErr: "template runs more than 1000000 steps"},
		{name: "doubling template calls fail at the step budget", text: `{{define "t0"}}{{end}}{{define "t1"}}{{template "t0"}}{{template "t0"}}{{end}}{{define "t2"}}{{template "t1"}}{{template "t1"}}{{end}}{{define "t3"}}{{template "t2"}}{{template "t2"}}{{end}}{{define "t4"}}{{template "t3"}}{{template "t3"}}{{end}}{{define "t5"}}{{template "t4"}}{{template "t4"}}{{end}}{{define "t6"}}{{template "t5"}}{{template "t5"}}{{end}}{{define "t7"}}{{template "t6"}}{{template "t6"}}{{end}}{{define "t8"}}{{template "t7"}}{{template "t7"}}{{end}}{{define "t9"}}{{template "t8"}}{{template "t8"}}{{end}}{{define "t10"}}{{template "t9"}}{{template "t9"}}{{end}}{{define "t11"}}{{template "t10"}}{{template "t10"}}{{end}}{{define "t12"}}{{template "t11"}}{{template "t11"}}{{end}}{{define "t13"}}{{template "t12"}}{{template "t12"}}{{end}}{{define "t14"}}{{template "t13"}}{{template "t13"}}{{end}}{{define "t15"}}{{template "t14"}}{{template "t14"}}{{end}}{{define "t16"}}{{template "t15"}}{{template "t15"}}{{end}}{{define "t17"}}{{template "t16"}}{{template "t16"}}{{end}}{{define "t18"}}{{template "t17"}}{{template "t17"}}{{end}}{{define "t19"}}{{template "t18"}}{{template "t18"}}{{end}}{{define "t20"}}{{template "t19"}}{{template "t19"}}{{end}}{{define "t21"}}{{template "t20"}}{{template "t20"}}{{end}}{{define "t22"}}{{template "t21"}}{{template "t21"}}{{end}}{{define "t23"}}{{template "t22"}}{{template "t22"}}{{end}}{{define "t24"}}{{template "t23"}}{{template "t23"}}{{end}}{{define "t25"}}{{template "t24"}}{{template "t24"}}{{end}}{{define "t26"}}{{template "t25"}}{{template "t25"}}{{end}}{{define "t27"}}{{template "t26"}}{{template "t26"}}{{end}}{{define "t28"}}{{template "t27"}}{{template "t27"}}{{end}}{{define "t29"}}{{template "t28"}}{{template "t28"}}{{end}}{{define "t30"}}{{template "t29"}}{{template "t29"}}{{end}}{{define "t31"}}{{template "t30"}}{{template "t30"}}{{end}}{{define "t32"}}{{template "t31"}}{{template "t31"}}{{end}}{{define "t33"}}{{template "t32"}}{{template "t32"}}{{end}}{{define "t34"}}{{template "t33"}}{{template "t33"}}{{end}}{{define "t35"}}{{template "t34"}}{{template "t34"}}{{end}}{{define "t36"}}{{template "t35"}}{{template "t35"}}{{end}}{{define "t37"}}{{template "t36"}}{{template "t36"}}{{end}}{{define "t38"}}{{template "t37"}}{{template "t37"}}{{end}}{{define "t39"}}{{template "t38"}}{{template "t38"}}{{end}}{{define "t40"}}{{template "t39"}}{{template "t39"}}{{end}}{{template "t40"}}`, wantErr: "template runs more than 1000000 steps"},
		{name: "range with a declaration renders", text: "{{range $i := 3}}{{$i}}{{end}}", want: "012"},
		{name: "range over vars renders", text: "{{range $k, $v := .Vars}}{{$k}}={{$v}}{{end}}", want: "a=b"},
		{name: "template call without data renders", text: `{{define "t"}}[{{.}}]{{end}}{{template "t"}}`, want: "[<no value>]"},
		{name: "template call with data renders", text: `{{define "t"}}[{{.}}]{{end}}{{template "t" .Variant}}`, want: "[v1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			got, err := execute("f.yaml", tt.text, templateData{Seed: tt.seed, Variant: "v1", Vars: map[string]string{"a": "b"}})
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Errorf("elapsed: got %v, want under 5s", elapsed)
			}
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if tt.wantErr == "" && gotErr != "" || tt.wantErr != "" && !strings.Contains(gotErr, tt.wantErr) {
				t.Fatalf("execute error: got %q, want one containing %q", gotErr, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("execute: got %q, want %q", got, tt.want)
			}
		})
	}
}
