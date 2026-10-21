package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// GET /api/settings — company details for invoices, plus whether email is set up.
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) error {
	co, err := s.companySettings(r.Context())
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"company": co, "email_configured": s.cfg.Mail.Configured(), "email_from": s.cfg.Mail.From})
}

// PUT /api/settings (admin)
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) error {
	var in CompanySettings
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.DefaultDueDays < 0 || in.DefaultDueDays > 365 {
		return errBad("default due days must be between 0 and 365")
	}
	if _, err := s.db.Exec(r.Context(), `
		UPDATE company_settings SET name=$1, address=$2, phone=$3, email=$4, invoice_footer=$5, default_due_days=$6, updated_at=now()
		WHERE id = 1`,
		strings.TrimSpace(in.Name), strings.TrimSpace(in.Address), strings.TrimSpace(in.Phone), strings.TrimSpace(in.Email),
		strings.TrimSpace(in.InvoiceFooter), in.DefaultDueDays); err != nil {
		return err
	}
	return s.getSettings(w, r)
}

// GET /api/invoices/{id}/pdf
func (s *Server) invoicePDF(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := s.invoiceDetail(r.Context(), s.db, id)
	if err != nil {
		return err
	}
	pdf, err := s.renderInvoicePDF(r.Context(), d)
	if err != nil {
		return err
	}
	disposition := "inline"
	if r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="invoice-%d.pdf"`, disposition, d.Invoice.Number))
	w.Header().Set("Cache-Control", "no-store")
	_, err = w.Write(pdf)
	return err
}

type sendInvoiceInput struct {
	To      string `json:"to"`
	Cc      string `json:"cc"`
	Subject string `json:"subject"`
	Message string `json:"message"`
}

// POST /api/invoices/{id}/send — emails the PDF to the client and marks a draft as sent.
func (s *Server) sendInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if !s.cfg.Mail.Configured() {
		return errConflict("email isn't set up on the server (SMTP_HOST / SMTP_FROM); download the PDF and send it yourself")
	}
	var in sendInvoiceInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	to, err := parseAddressList("to", in.To, true)
	if err != nil {
		return err
	}
	cc, err := parseAddressList("cc", in.Cc, false)
	if err != nil {
		return err
	}
	ctx := r.Context()
	d, err := s.invoiceDetail(ctx, s.db, id)
	if err != nil {
		return err
	}
	if d.Invoice.Status == "void" {
		return errConflict("void invoices can't be sent")
	}
	if len(d.Lines) == 0 {
		return errBad("the invoice has no lines")
	}
	co, err := s.companySettings(ctx)
	if err != nil {
		return err
	}
	pdf, err := s.renderInvoicePDF(ctx, d)
	if err != nil {
		return err
	}
	subject := strings.TrimSpace(in.Subject)
	if subject == "" {
		subject = fmt.Sprintf("Invoice %d from %s", d.Invoice.Number, co.Name)
	}
	if len(subject) > 200 {
		return errBad("subject is too long")
	}
	msg := &outgoingMail{
		To: to, Cc: cc, ReplyTo: co.Email, Subject: subject, Text: in.Message,
		Attachments: []attachment{{Name: fmt.Sprintf("invoice-%d.pdf", d.Invoice.Number), ContentType: "application/pdf", Data: pdf}},
	}
	if err := s.cfg.Mail.send(*msg); err != nil {
		slog.Error("send invoice email", "invoice", id, "err", err)
		return &apiError{http.StatusBadGateway, "the email could not be sent: " + err.Error()}
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE invoices SET sent_at = now(), sent_to = $2, status = CASE WHEN status = 'draft' THEN 'sent' ELSE status END, updated_at = now()
		WHERE id = $1`, id, strings.Join(append(to, cc...), ", ")); err != nil {
		return err
	}
	d, err = s.invoiceDetail(ctx, s.db, id)
	if err != nil {
		return err
	}
	return ok(w, d)
}
