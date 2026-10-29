package presets_test

import (
	"bufio"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

// SplitWire presets2.txt with the Windows-only --wf-* tokens removed.
var splitwire = []string{
	"--payload=tls_client_hello --lua-desync=multidisorder:pos=2:seqovl=1",
	"--payload=tls_client_hello --lua-desync=multisplit:blob=fake_default_tls:ip_ttl=5:pos=2:nodrop:repeats=1",
	"--payload=tls_client_hello --lua-desync=fake:blob=0x00000000:ip_ttl=5:repeats=1",
	"--in-range=-s1 --lua-desync=oob:urp=b",
	"--in-range=-s1 --lua-desync=oob:urp=0",
	"--payload=http_req --lua-desync=http_hostcase:spell=hoSt",
	"--payload=http_req --lua-desync=multidisorder:pos=midsld:seqovl=midsld-1",
	"--payload=http_req --lua-desync=multidisorder:pos=method+2,midsld",
	"--payload=http_req --lua-desync=multidisorder:pos=method+2:seqovl=method+1",
	"--payload=http_req --lua-desync=multidisorder:pos=method+2:seqovl=method+1:seqovl_pattern=fake_default_http",
}

func TestValidArgs(t *testing.T) {
	valid := slices.Concat(splitwire, []string{
		"--payload=stun,discord_ip_discovery --lua-desync=fake:blob=fake_default_quic:repeats=6", // Discord voice
		"--payload=quic_initial --lua-desync=fake:blob=fake_default_quic:repeats=6",              // QUIC
		"--blob=x:0xdeadbeef --lua-desync=fake:blob=x",
	})
	for _, s := range valid {
		if err := presets.ValidateArgs(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
}

// Every strategy blockcheck2 tried in the bc-simulate.log fixture passes, except the
// 9 that carry inline Lua (--lua-init=<code>, luaexec:code=).
func TestBlockcheckTails(t *testing.T) {
	f, err := os.Open("../scan/testdata/bc-simulate.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tails := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if _, tail, ok := strings.Cut(sc.Text(), ": nfqws2 "); ok && strings.HasPrefix(sc.Text(), "- ") {
			tails[tail] = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	inline := 0
	for tail := range tails {
		err := presets.ValidateArgs(tail)
		if strings.Contains(tail, "--lua-init") || strings.Contains(tail, "luaexec") {
			inline++
			if err == nil {
				t.Errorf("inline Lua accepted: %q", tail)
			}
		} else if err != nil {
			t.Errorf("%q: %v", tail, err)
		}
	}
	if len(tails) != 1554 || inline != 9 {
		t.Errorf("got %d tails, %d inline; want 1554, 9", len(tails), inline)
	}
}

func TestInvalidArgs(t *testing.T) {
	for _, s := range []string{
		"--lua-init=@/tmp/x.lua",
		"--lua-init=fake_default_tls=tls_mod(fake_default_tls,'rnd') --payload=tls_client_hello --lua-desync=multidisorder:pos=2:seqovl=1:seqovl_pattern=fake_default_tls",
		"--lua-desync=luaexec:code=desync.patmod=tls_mod(fake_default_tls,'rnd,dupsid,padencap',desync.reasm_data)",
		"--lua-desync=luaexec:code=x",
		"--blob=x:@/etc/passwd",
		"--blob=x:deadbeef",
		"--lua-desync=fake:blob=@/etc/shadow",
		"--payload=tls_client_hello; rm -rf /",
		"--lua-desync=no_such_func:x=1",
		"--filter-tcp=443",
		"--hostlist=/etc/passwd",
		"--user=root",
		"--debug=1",
		`--lua-desync="fake"`,
		"",
		// blob arguments must not reach arbitrary Lua globals
		"--lua-desync=fake:blob=desync",
		"--lua-desync=multisplit:pos=2:seqovl_pattern=patmod",
		"--lua-desync=fakedsplit:pos=1:pattern=os",
		// < only belongs in ranges
		"--payload=tls_client_hello<x --lua-desync=fake",
		"--payload=tls_client_hello  --lua-desync=fake",
	} {
		if err := presets.ValidateArgs(s); err == nil {
			t.Errorf("%q: accepted", s)
		}
	}
}

// Arguments that zapret2 resolves as Lua globals or code.
func TestLuaNameArgs(t *testing.T) {
	for _, s := range []string{
		"--lua-desync=condition:iff=cond_random:percent=50 --lua-desync=fake:blob=fake_default_tls",
		"--lua-desync=stopif:iff=cond_false:neg",
		"--lua-desync=repeater:repeats=2 --lua-desync=fake:blob=fake_default_tls",
		"--lua-desync=repeater:repeats=2:iff=cond_tcp_has_ts",
		"--lua-desync=per_instance_condition --lua-desync=fake:blob=fake_default_tls:cond=cond_true:cond_neg",
		"--lua-desync=circular:hostkey=standard_hostkey:failure_detector=standard_failure_detector:success_detector=standard_success_detector --lua-desync=fake:blob=fake_default_tls:strategy=1",
		"--lua-desync=send:ipfrag:ipfrag_pos_udp=16",
		"--lua-desync=send:ipfrag=ipfrag2",
		"--lua-desync=tls_client_hello_clone:blob=fake_default_tls:fallback=fake_default_tls",
	} {
		if err := presets.ValidateArgs(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range []string{
		"--lua-desync=condition:iff=cond_lua:cond_code=x=1",
		"--lua-desync=condition:iff=cond_true:cond_code=x=1",
		"--lua-desync=condition:iff=os.execute",
		"--lua-desync=condition:iff=luaexec",
		"--lua-desync=condition:iff=load",
		"--lua-desync=stopif:iff=dofile",
		"--lua-desync=repeater:repeats=2:iff=require",
		"--lua-desync=per_instance_condition --lua-desync=fake:blob=fake_default_tls:cond=load",
		"--lua-desync=per_instance_condition --lua-desync=fake:blob=fake_default_tls:cond=cond_lua",
		"--lua-desync=circular:hostkey=load",
		"--lua-desync=circular:failure_detector=luaexec",
		"--lua-desync=circular:success_detector=os.exit",
		"--lua-desync=send:fool=os.execute",
		"--lua-desync=fake:blob=fake_default_tls:fool=luaexec",
		"--lua-desync=send:ipfrag=load",
		"--lua-desync=tls_client_hello_clone:blob=fake_default_tls:fallback=desync",
		"--lua-desync=fake:blob=fake_default_tls:code=x",
	} {
		if err := presets.ValidateArgs(s); err == nil {
			t.Errorf("%q: accepted", s)
		}
	}
}

func TestValidatePorts(t *testing.T) {
	for _, s := range []string{"50000-65535", "443", "50000-50100,19294-19344,3478-3481,5349,1400"} {
		if err := presets.ValidatePorts(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range []string{"0-10", "70000", "1-2-3", "", "10-5", "80,", "a"} {
		if err := presets.ValidatePorts(s); err == nil {
			t.Errorf("%q: accepted", s)
		}
	}
}

func TestLuaAllowListPinned(t *testing.T) {
	if presets.LuaFuncsEngine != engine.Version {
		t.Errorf("allow-list from %s, engine pinned to %s: rerun go generate", presets.LuaFuncsEngine, engine.Version)
	}
	for _, f := range []string{"fake", "multidisorder", "oob", "circular"} {
		if !presets.LuaFuncs[f] {
			t.Errorf("%s missing from allow-list", f)
		}
	}
	for _, f := range []string{"luaexec", "pass", "orchestrate"} {
		if presets.LuaFuncs[f] {
			t.Errorf("zapret-lib function %s in allow-list", f)
		}
	}
}

func TestLoadSkipsInvalid(t *testing.T) {
	fsys := fixture()
	fsys["apps/badhost.toml"] = file("schema = 1\nid = \"badhost\"\nname = \"B\"\nhosts = [\"bad host\"]\n")
	fsys["apps/badudp.toml"] = file("schema = 1\nid = \"badudp\"\nname = \"U\"\nhosts = [\"u.com\"]\n[udp]\nports = \"0-10\"\n")
	fsys["isp/tr/as1-evil.toml"] = file("schema = 1\nname = \"Evil\"\nasn = [1]\n[[strategy]]\nid = \"e\"\ntcp_tls = \"--lua-init=@/tmp/x.lua\"\n")
	s := presets.Load(fsys)
	for _, f := range []string{"apps/badhost.toml", "apps/badudp.toml", "isp/tr/as1-evil.toml"} {
		if !slices.ContainsFunc(s.Warnings, func(w string) bool { return strings.HasPrefix(w, f+": ") }) {
			t.Errorf("no warning for %s: %q", f, s.Warnings)
		}
	}
	if _, ok := s.Apps["badhost"]; ok {
		t.Error("app with a bad host was loaded")
	}
	if _, ok := s.Apps["badudp"]; ok {
		t.Error("app with bad UDP ports was loaded")
	}
	if len(s.Candidates(1, "XX")) != 1 {
		t.Errorf("evil ISP strategy loaded: %+v", s.Candidates(1, "XX"))
	}
	if len(s.Warnings) != 5 {
		t.Errorf("warnings: %q, want 5", s.Warnings)
	}
}
