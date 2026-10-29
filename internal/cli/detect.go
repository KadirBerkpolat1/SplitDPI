package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

func init() {
	commands["detect"] = Command{Name: "detect", Root: true, Help: "cmd.detect.help", Run: runDetect}
}

func runDetect(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	asn, country, err := parseDetectArgs(args)
	if err != nil {
		return usageError(i18n.T("detect.usage"))
	}
	if asn == 0 && country == "" {
		host, err := o.IPInfoHost()
		if err != nil {
			return err
		}
		fmt.Fprintln(w, i18n.T("detect.asking", host))
	}
	info, cands, err := o.Detect(ctx, asn, country)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, i18n.T("detect.result", info.ASN, info.Country, info.Org))
	if info.Org != "" {
		fmt.Fprintln(w, i18n.T("detect.isp", info.Org))
	}
	fmt.Fprintln(w, i18n.T("detect.candidates"))
	for i, c := range cands {
		fmt.Fprintf(w, "  %d. %s (%s)\n", i+1, c.Strategy.ID, c.Source)
	}
	return nil
}

func parseDetectArgs(args []string) (asn int, country string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--asn":
			if i+1 >= len(args) {
				return 0, "", fmt.Errorf("missing")
			}
			asn, err = strconv.Atoi(args[i+1])
			if err != nil || asn <= 0 {
				return 0, "", fmt.Errorf("bad asn")
			}
			i++
		case "--country":
			if i+1 >= len(args) {
				return 0, "", fmt.Errorf("missing")
			}
			country = strings.ToUpper(args[i+1])
			i++
		default:
			return 0, "", fmt.Errorf("unknown")
		}
	}
	return asn, country, nil
}
