package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func sampleInvoice() *InvoiceDetail {
	mins := int64(2820)
	pid := int64(1)
	pname := "Hyrum 23"
	client := "Demo Builder Co."
	due := "2026-06-03"
	start, end := "2026-04-27", "2026-05-03"
	return &InvoiceDetail{
		Invoice: Invoice{ID: 4, Number: 4, ClientName: &client, IssueDate: "2026-05-04", DueDate: &due, PeriodStart: &start,
			PeriodEnd: &end, Status: "sent", TotalCents: 164500 + 12050, Minutes: 2820, Notes: "Thanks — call with any questions.",
			Workers: []WorkerMinutes{{WorkerID: 1, WorkerName: "Justin M", Minutes: 2820}}},
		Lines: []InvoiceLine{
			{ID: 1, ProjectID: &pid, ProjectName: &pname, Kind: "hours", Description: "Hyrum 23 — labor", Minutes: &mins, Quantity: 47, UnitPriceCents: 3500, AmountCents: 164500},
			{ID: 2, Kind: "other", Description: strings.Repeat("Drip edge and fasteners, ", 8), Quantity: 1, UnitPriceCents: 12050, AmountCents: 12050},
		},
	}
}

func TestDrawInvoicePDF(t *testing.T) {
	pdf, err := drawInvoicePDF(
		CompanySettings{Name: "M&M Roofing", Address: "12 Main St\nHyrum, UT 84319", Phone: "435-555-0100", Email: "office@example.com", InvoiceFooter: "Pay by check to M&M Roofing."},
		clientContact{Name: "Demo Builder Co.", ContactName: "Pat", Address: "1 Builder Way"},
		sampleInvoice())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || len(pdf) < 1000 {
		t.Fatalf("not a PDF (%d bytes)", len(pdf))
	}
	if out := os.Getenv("INVOICE_PDF_OUT"); out != "" {
		os.WriteFile(out, pdf, 0o644)
	}
}

func TestBuildMessage(t *testing.T) {
	msg, err := buildMessage("Billing <billing@example.com>", outgoingMail{
		To: []string{"client@example.com"}, Subject: "Invoice 4 — May", Text: "Hi,\nattached.",
		Attachments: []attachment{{Name: "invoice-4.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.4 test")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(msg)
	for _, want := range []string{"To: client@example.com\r\n", "Subject: =?utf-8?q?", `filename=invoice-4.pdf`, "Content-Type: application/pdf"} {
		if !strings.Contains(s, want) {
			t.Errorf("message missing %q", want)
		}
	}
	if _, err := buildMessage("a@b.c", outgoingMail{Subject: "x\r\nBcc: evil@example.com"}); err == nil {
		t.Error("header injection not rejected")
	}
}

func TestParseAddressList(t *testing.T) {
	got, err := parseAddressList("to", "a@example.com; Bob <b@example.com>, ", true)
	if err != nil || len(got) != 2 || got[1] != "b@example.com" {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := parseAddressList("to", "not-an-email", true); err == nil {
		t.Error("invalid address accepted")
	}
	if _, err := parseAddressList("to", " ", true); err == nil {
		t.Error("empty required list accepted")
	}
}

func TestInternalOIDCRewrite(t *testing.T) {
	var gotHost, gotProto string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotProto = r.Host, r.Header.Get("X-Forwarded-Proto")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := &oidcClient{cfg: Config{OIDCIssuer: "https://auth.clayton-montgomery.dev", OIDCInternalURL: srv.URL}}
	resp, err := c.httpClient().Get("https://auth.clayton-montgomery.dev/.well-known/openid-configuration")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotHost != "auth.clayton-montgomery.dev" || gotProto != "https" {
		t.Fatalf("host=%q proto=%q", gotHost, gotProto)
	}
}
