// Command b3unsubtest is a throwaway spike tool for email-marketing spike B3:
// does Resend's /emails endpoint put a List-Unsubscribe header inside the DKIM
// h= signed set? Gmail/Yahoo hide the one-click Unsubscribe button (RFC 8058) if
// List-Unsubscribe / List-Unsubscribe-Post are NOT DKIM-covered, and Resend's
// docs don't state whether they are — so this must be settled by a real send.
//
// It never sees or prints your API key: it reads RESEND_API_KEY from the
// environment and uses it only in the Authorization header. Safe to delete after
// the spike (it is not part of the server binary — main.go is built alone).
//
// Usage (run from crm-backend/):
//
//	# Send both variants to a Gmail inbox you control:
//	#   Variant A = self-supplied List-Unsubscribe + List-Unsubscribe-Post headers
//	#   Variant B = Resend-managed via topic_id (no custom header)
//	RESEND_API_KEY=re_xxx go run ./cmd/b3unsubtest send \
//	    -from "Marketing Test <noreply@send.YOURDOMAIN.com>" \
//	    -to you@gmail.com \
//	    -unsub "https://YOURCRM.example.com/api/marketing/u/TESTTOKEN" \
//	    -topic topic_xxxxxxxx           # omit -topic to run variant A only
//
//	# Then in Gmail: open each message -> ⋮ -> "Download original" (an .eml),
//	# and check the DKIM h= coverage:
//	go run ./cmd/b3unsubtest check -file "b3-test-a.eml"
//	#   ...or pipe it:  Get-Content b3-test-a.eml | go run ./cmd/b3unsubtest check
//
// PASS (for the send path to be viable): the check reports list-unsubscribe AND
// list-unsubscribe-post are both inside a dkim=pass signature's h= tag.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "send":
		cmdSend(os.Args[2:])
	case "check":
		cmdCheck(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `b3unsubtest — Resend List-Unsubscribe DKIM-coverage spike (B3)

  send   POST test email(s) to a real inbox (reads RESEND_API_KEY from env)
  check  parse a received .eml / raw source and report whether List-Unsubscribe
         is inside the DKIM h= signed set (PASS/FAIL)

Run 'go run ./cmd/b3unsubtest send -h' or '... check -h' for flags.`)
}

// ── send ─────────────────────────────────────────────────────────────────────

func cmdSend(args []string) {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	from := fs.String("from", "", "From address on a Resend-VERIFIED domain, e.g. \"Name <noreply@send.you.com>\" (required)")
	to := fs.String("to", "", "recipient inbox you control, ideally Gmail AND (separately) Yahoo (required)")
	unsub := fs.String("unsub", "", "https URL for the one-click List-Unsubscribe (variant A). Omit to skip variant A")
	mailto := fs.String("mailto", "", "optional mailto: unsubscribe address added to List-Unsubscribe (variant A)")
	topic := fs.String("topic", "", "Resend topic_id for the managed path (variant B). Omit to skip variant B")
	subjectPrefix := fs.String("subject-prefix", "B3 DKIM test", "email subject prefix")
	_ = fs.Parse(args)

	apiKey := strings.TrimSpace(os.Getenv("RESEND_API_KEY"))
	if apiKey == "" {
		fatal("RESEND_API_KEY is not set. Export it in your shell first — this tool never prints it.")
	}
	if *from == "" || *to == "" {
		fatal("-from and -to are required.")
	}
	if *unsub == "" && *topic == "" {
		fatal("nothing to send: provide -unsub (variant A) and/or -topic (variant B).")
	}

	// Variant A — self-supplied RFC 8058 one-click headers.
	if *unsub != "" {
		listUnsub := "<" + *unsub + ">"
		if *mailto != "" {
			listUnsub += ", <mailto:" + *mailto + ">"
		}
		payload := map[string]any{
			"from":    *from,
			"to":      []string{*to},
			"subject": *subjectPrefix + " — Variant A (self-supplied headers)",
			"html":    "<p>B3 variant A: self-supplied <code>List-Unsubscribe</code> + <code>List-Unsubscribe-Post</code>. If your client shows a one-click Unsubscribe by the sender, and the DKIM h= check passes, this path is viable.</p>",
			"headers": map[string]string{
				"List-Unsubscribe":      listUnsub,
				"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
			},
		}
		send(apiKey, "Variant A (self-supplied headers)", payload)
	}

	// Variant B — Resend-managed via topic_id, no custom header. NOTE: the recipient
	// must be a contact opted-in to the topic, OR the topic's default must be opt-in,
	// or Resend marks the send failed (see the topic_id docs).
	if *topic != "" {
		payload := map[string]any{
			"from":     *from,
			"to":       []string{*to},
			"subject":  *subjectPrefix + " — Variant B (topic_id managed)",
			"html":     "<p>B3 variant B: no custom header — relying on Resend's topic_id managed one-click. Check whether Resend injected List-Unsubscribe and whether it is DKIM-covered.</p>",
			"topic_id": *topic,
		}
		send(apiKey, "Variant B (topic_id managed)", payload)
	}

	fmt.Println("\nNext: in your inbox, open each message → \"Download original\" (.eml), then run:")
	fmt.Println("  go run ./cmd/b3unsubtest check -file <that-file>.eml")
	fmt.Println("Repeat against a Yahoo inbox too — Yahoo enforces one-click independently of Gmail.")
}

func send(apiKey, label string, payload map[string]any) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		fatal("build request: " + err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fatal("send " + label + ": " + err.Error())
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))

	fmt.Printf("── %s\n", label)
	fmt.Printf("   HTTP %d\n", resp.StatusCode)
	fmt.Printf("   response: %s\n", strings.TrimSpace(string(rb)))
	if resp.StatusCode >= 300 {
		fmt.Printf("   ⚠️  non-2xx — fix this before trusting the result (e.g. topic opt-in, unverified domain).\n")
	}
}

// ── check ────────────────────────────────────────────────────────────────────

var hTagRe = regexp.MustCompile(`(?is)\bh\s*=\s*([^;]+)`)
var dTagRe = regexp.MustCompile(`(?is)\bd\s*=\s*([^;]+)`)

func cmdCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	file := fs.String("file", "", "path to a saved .eml / raw email source; omit to read from stdin")
	_ = fs.Parse(args)

	var raw []byte
	var err error
	if *file != "" {
		raw, err = os.ReadFile(*file)
		if err != nil {
			fatal("read " + *file + ": " + err.Error())
		}
	} else {
		raw, err = io.ReadAll(os.Stdin)
		if err != nil {
			fatal("read stdin: " + err.Error())
		}
	}

	dkimSigs := grabHeaders(raw, "dkim-signature")
	authResults := grabHeaders(raw, "authentication-results")
	luHeaders := grabHeaders(raw, "list-unsubscribe")
	lupHeaders := grabHeaders(raw, "list-unsubscribe-post")

	fmt.Println("── B3 DKIM-coverage check ───────────────────────────────")
	fmt.Printf("List-Unsubscribe header present:       %v\n", boolMark(len(luHeaders) > 0))
	fmt.Printf("List-Unsubscribe-Post header present:  %v\n", boolMark(len(lupHeaders) > 0))

	authPass := false
	for _, ar := range authResults {
		if strings.Contains(strings.ToLower(ar), "dkim=pass") {
			authPass = true
		}
	}
	fmt.Printf("Authentication-Results dkim=pass:      %v\n", boolMark(authPass))

	if len(dkimSigs) == 0 {
		fmt.Println("\n❌ No DKIM-Signature header found. Make sure you saved the FULL raw source (.eml), not the rendered email.")
		os.Exit(1)
	}

	luCovered, lupCovered := false, false
	fmt.Printf("\nDKIM-Signature header(s) found: %d\n", len(dkimSigs))
	for i, sig := range dkimSigs {
		domain := firstGroup(dTagRe, sig)
		signed := parseHTag(sig)
		fmt.Printf("  [%d] d=%s  signs h=[%s]\n", i+1, orDash(domain), strings.Join(signed, " : "))
		for _, h := range signed {
			switch h {
			case "list-unsubscribe":
				luCovered = true
			case "list-unsubscribe-post":
				lupCovered = true
			}
		}
	}

	fmt.Println("\n── Verdict ──────────────────────────────────────────────")
	fmt.Printf("list-unsubscribe in DKIM h=:       %v\n", boolMark(luCovered))
	fmt.Printf("list-unsubscribe-post in DKIM h=:  %v\n", boolMark(lupCovered))

	if luCovered && lupCovered && authPass {
		fmt.Println("\n✅ PASS — both List-Unsubscribe headers are inside a DKIM-signed set and DKIM passes.")
		fmt.Println("   The /emails send path is viable for one-click unsubscribe (M3/M7 proceed on /emails).")
		return
	}
	fmt.Println("\n❌ FAIL — one-click is NOT reliably DKIM-covered on this path.")
	if !luCovered || !lupCovered {
		fmt.Println("   The List-Unsubscribe header(s) are not in the DKIM h= set → Gmail/Yahoo will hide the button.")
	}
	if !authPass {
		fmt.Println("   DKIM did not pass — verify the sending domain's DKIM DNS before trusting the h= result.")
	}
	fmt.Println("   If both variants fail, route bulk through Resend Broadcasts (loses per-recipient CRM merge tags).")
	os.Exit(1)
}

// grabHeaders returns every occurrence of the named header (case-insensitive),
// unfolded (RFC 5322 continuation lines starting with whitespace are joined).
func grabHeaders(raw []byte, name string) []string {
	name = strings.ToLower(name)
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var cur strings.Builder
	capturing := false
	flush := func() {
		if capturing && cur.Len() > 0 {
			out = append(out, cur.String())
		}
		cur.Reset()
		capturing = false
	}
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if capturing {
				cur.WriteString(" ")
				cur.WriteString(strings.TrimSpace(line))
			}
			continue
		}
		// A new header line (or noise). Close any header we were capturing.
		flush()
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		if strings.ToLower(strings.TrimSpace(line[:colon])) == name {
			capturing = true
			cur.WriteString(strings.TrimSpace(line[colon+1:]))
		}
	}
	flush()
	return out
}

// parseHTag extracts and normalizes the h= tag of a DKIM-Signature into a list of
// lowercase header names.
func parseHTag(sig string) []string {
	m := hTagRe.FindStringSubmatch(sig)
	if m == nil {
		return nil
	}
	raw := strings.ReplaceAll(m[1], " ", "")
	raw = strings.ReplaceAll(raw, "\t", "")
	var out []string
	for _, h := range strings.Split(raw, ":") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			out = append(out, h)
		}
	}
	return out
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// ── helpers ────────────────────────────────────────────────────────────────

func boolMark(b bool) string {
	if b {
		return "yes ✅"
	}
	return "no ❌"
}
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	os.Exit(1)
}
