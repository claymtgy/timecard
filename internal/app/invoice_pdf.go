package app

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/claym/crm/internal/calc"
	"github.com/go-pdf/fpdf"
)

type CompanySettings struct {
	Name           string `db:"name" json:"name"`
	Address        string `db:"address" json:"address"`
	Phone          string `db:"phone" json:"phone"`
	Email          string `db:"email" json:"email"`
	InvoiceFooter  string `db:"invoice_footer" json:"invoice_footer"`
	DefaultDueDays int64  `db:"default_due_days" json:"default_due_days"`
}

func (s *Server) companySettings(ctx context.Context) (CompanySettings, error) {
	return one[CompanySettings](ctx, s.db, `
		SELECT name, address, phone, email, invoice_footer, default_due_days::bigint FROM company_settings WHERE id = 1`)
}

type clientContact struct {
	Name        string `db:"name"`
	ContactName string `db:"contact_name"`
	Email       string `db:"email"`
	Phone       string `db:"phone"`
	Address     string `db:"address"`
}

func pdfDate(s *string) string {
	if s == nil || *s == "" {
		return ""
	}
	t, err := time.Parse(calc.DateLayout, *s)
	if err != nil {
		return *s
	}
	return t.Format("Jan 2, 2006")
}

func pdfHours(minutes int64) string {
	h := math.Round(float64(minutes)/60*100) / 100
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", h), "0"), ".")
}

// renderInvoicePDF draws a one-or-more page US Letter invoice.
func (s *Server) renderInvoicePDF(ctx context.Context, d *InvoiceDetail) ([]byte, error) {
	co, err := s.companySettings(ctx)
	if err != nil {
		return nil, err
	}
	var cl clientContact
	if d.Invoice.ClientID != nil {
		if cl, err = one[clientContact](ctx, s.db, `SELECT name, contact_name, email, phone, address FROM clients WHERE id = $1`, *d.Invoice.ClientID); err != nil {
			return nil, err
		}
	}
	return drawInvoicePDF(co, cl, d)
}

// drawInvoicePDF lays out the invoice; kept free of I/O so it can be tested.
func drawInvoicePDF(co CompanySettings, cl clientContact, d *InvoiceDetail) ([]byte, error) {
	inv := d.Invoice

	pdf := fpdf.New("P", "pt", "Letter", "")
	pdf.SetTitle(fmt.Sprintf("Invoice %d", inv.Number), true)
	pdf.SetAuthor(co.Name, true)
	pdf.SetMargins(48, 48, 48)
	pdf.SetAutoPageBreak(true, 60)
	tr := pdf.UnicodeTranslatorFromDescriptor("") // UTF-8 -> cp1252 for the core fonts
	pdf.SetFooterFunc(func() {
		pdf.SetY(-40)
		pdf.SetFont("Helvetica", "", 8)
		pdf.SetTextColor(130, 130, 130)
		pdf.CellFormat(0, 10, tr(fmt.Sprintf("Invoice %d  ·  page %d of {nb}", inv.Number, pdf.PageNo())), "", 0, "C", false, 0, "")
	})
	pdf.AliasNbPages("")
	pdf.AddPage()
	pageW, _ := pdf.GetPageSize()
	left, _, right, _ := pdf.GetMargins()
	contentW := pageW - left - right

	// Header: company on the left, "INVOICE" block on the right.
	pdf.SetTextColor(20, 24, 33)
	pdf.SetFont("Helvetica", "B", 18)
	name := co.Name
	if name == "" {
		name = "Invoice"
	}
	pdf.CellFormat(contentW/2, 22, tr(name), "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "B", 22)
	pdf.SetTextColor(47, 111, 237)
	pdf.CellFormat(contentW/2, 22, "INVOICE", "", 1, "R", false, 0, "")
	pdf.SetTextColor(90, 98, 112)
	pdf.SetFont("Helvetica", "", 9.5)
	y := pdf.GetY() + 4
	pdf.SetXY(left, y)
	for _, line := range nonEmpty(append(strings.Split(co.Address, "\n"), co.Phone, co.Email)...) {
		pdf.CellFormat(contentW/2, 13, tr(strings.TrimSpace(line)), "", 2, "L", false, 0, "")
	}
	yLeft := pdf.GetY()
	meta := [][2]string{{"Invoice #", fmt.Sprint(inv.Number)}, {"Issued", pdfDate(&inv.IssueDate)}}
	if inv.DueDate != nil {
		meta = append(meta, [2]string{"Due", pdfDate(inv.DueDate)})
	}
	if inv.PeriodStart != nil || inv.PeriodEnd != nil {
		meta = append(meta, [2]string{"Work period", strings.Trim(pdfDate(inv.PeriodStart)+" – "+pdfDate(inv.PeriodEnd), " –")})
	}
	pdf.SetXY(left+contentW/2, y)
	for _, m := range meta {
		pdf.SetX(left + contentW/2)
		pdf.SetFont("Helvetica", "", 9.5)
		pdf.CellFormat(contentW/4, 13, tr(m[0]), "", 0, "R", false, 0, "")
		pdf.SetFont("Helvetica", "B", 9.5)
		pdf.SetTextColor(20, 24, 33)
		pdf.CellFormat(contentW/4, 13, tr(m[1]), "", 1, "R", false, 0, "")
		pdf.SetTextColor(90, 98, 112)
	}
	pdf.SetY(math.Max(yLeft, pdf.GetY()) + 18)

	// Bill to.
	pdf.SetFont("Helvetica", "B", 8.5)
	pdf.CellFormat(0, 12, "BILL TO", "", 1, "L", false, 0, "")
	pdf.SetTextColor(20, 24, 33)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 15, tr(cl.Name), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9.5)
	for _, line := range nonEmpty(append([]string{cl.ContactName}, append(strings.Split(cl.Address, "\n"), cl.Email)...)...) {
		pdf.CellFormat(0, 13, tr(strings.TrimSpace(line)), "", 1, "L", false, 0, "")
	}
	pdf.Ln(16)

	// Line items.
	cols := []float64{contentW - 250, 70, 90, 90}
	pdf.SetFillColor(240, 242, 245)
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetTextColor(90, 98, 112)
	for i, h := range []string{"Description", "Qty", "Rate", "Amount"} {
		align := "R"
		if i == 0 {
			align = "L"
		}
		pdf.CellFormat(cols[i], 20, h, "", 0, align, true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetTextColor(20, 24, 33)
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetDrawColor(225, 228, 234)
	for _, l := range d.Lines {
		desc := wrapText(pdf, tr, l.Description, cols[0]-10)
		h := math.Max(20, float64(len(desc))*13+7)
		if pdf.GetY()+h > 792-70 {
			pdf.AddPage()
		}
		x, y := pdf.GetXY()
		pdf.SetXY(x+4, y+4)
		pdf.MultiCell(cols[0]-8, 13, strings.Join(desc, "\n"), "", "L", false)
		qty, rate := fmt.Sprintf("%g", l.Quantity), formatCents(l.UnitPriceCents)
		if l.Kind == "hours" && l.Minutes != nil {
			qty, rate = pdfHours(*l.Minutes)+" h", rate+"/h"
		}
		pdf.SetXY(x+cols[0], y)
		pdf.CellFormat(cols[1], 20, tr(qty), "", 0, "R", false, 0, "")
		pdf.CellFormat(cols[2], 20, tr(rate), "", 0, "R", false, 0, "")
		pdf.CellFormat(cols[3], 20, tr(formatCents(l.AmountCents)), "", 0, "R", false, 0, "")
		pdf.Line(left, y+h, left+contentW, y+h)
		pdf.SetXY(left, y+h)
	}

	// Totals.
	pdf.Ln(6)
	total := func(label, value string, bold bool) {
		style := ""
		if bold {
			style = "B"
		}
		pdf.SetFont("Helvetica", style, 10.5)
		pdf.SetX(left + contentW - 250)
		pdf.CellFormat(160, 18, tr(label), "", 0, "R", false, 0, "")
		pdf.CellFormat(90, 18, tr(value), "", 1, "R", false, 0, "")
	}
	total("Total", formatCents(inv.TotalCents), false)
	if inv.Status == "paid" {
		total("Paid "+pdfDate(inv.PaidDate), "-"+formatCents(inv.TotalCents), false)
		total("Balance due", formatCents(0), true)
	} else {
		total("Balance due", formatCents(inv.TotalCents), true)
	}

	// Labor summary (man-hours by worker), then notes and payment instructions.
	if len(inv.Workers) > 0 {
		pdf.Ln(10)
		pdf.SetFont("Helvetica", "", 8.5)
		pdf.SetTextColor(90, 98, 112)
		var parts []string
		for _, w := range inv.Workers {
			parts = append(parts, fmt.Sprintf("%s %s h", w.WorkerName, pdfHours(w.Minutes)))
		}
		pdf.MultiCell(0, 12, tr("Labor: "+strings.Join(parts, "  ·  ")+fmt.Sprintf("  ·  total %s man-hours", pdfHours(inv.Minutes))), "", "L", false)
	}
	for _, block := range []struct{ title, body string }{{"Notes", inv.Notes}, {"Payment", co.InvoiceFooter}} {
		if strings.TrimSpace(block.body) == "" {
			continue
		}
		pdf.Ln(12)
		pdf.SetTextColor(90, 98, 112)
		pdf.SetFont("Helvetica", "B", 8.5)
		pdf.CellFormat(0, 12, strings.ToUpper(block.title), "", 1, "L", false, 0, "")
		pdf.SetTextColor(20, 24, 33)
		pdf.SetFont("Helvetica", "", 9.5)
		pdf.MultiCell(0, 13, tr(block.body), "", "L", false)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// wrapText word-wraps UTF-8 text to width using the current (core) font and returns
// lines already translated to the font's code page. fpdf's SplitText can't be used:
// it indexes the 256-entry width table by Unicode code point.
func wrapText(pdf *fpdf.Fpdf, tr func(string) string, text string, width float64) []string {
	var lines []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			candidate := strings.TrimSpace(line + " " + word)
			if pdf.GetStringWidth(tr(candidate)) <= width {
				line = candidate
				continue
			}
			if line != "" {
				lines = append(lines, tr(line))
			}
			line = ""
			// Hard-split words wider than the column.
			for _, r := range word {
				if pdf.GetStringWidth(tr(line+string(r))) > width && line != "" {
					lines = append(lines, tr(line))
					line = ""
				}
				line += string(r)
			}
		}
		lines = append(lines, tr(line))
	}
	return lines
}
