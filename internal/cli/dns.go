package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

func init() {
	commands["dns"] = Command{Name: "dns", Root: true, Help: "cmd.dns.help", Run: runDNS}
}

func runDNS(_ context.Context, o *ops.Ops, w io.Writer, args []string) error {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return usageError(i18n.T("dns.usage"))
	}
	on := args[0] == "on"
	running, err := o.DNS(on)
	if err != nil {
		return err
	}
	if on {
		fmt.Fprintln(w, i18n.T("dns.on"))
		fmt.Fprintln(w, i18n.T("status.dns"))
	} else {
		fmt.Fprintln(w, i18n.T("dns.off"))
	}
	if running {
		fmt.Fprintln(w, i18n.T("dns.restart"))
	}
	return nil
}
