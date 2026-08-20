// Package mail отправляет письма через внешний SMTP-релей.
//
// Реализация — только стандартная библиотека: первая внешняя зависимость
// потребовала бы менять go.mod и Dockerfile (см. CLAUDE.md), а для одного
// письма с ссылкой подтверждения это не нужно.
package mail

import (
	"fmt"
	"mime"
	"net/smtp"
	"strings"
)

// Sender отправляет письмо. Интерфейс существует, чтобы HTTP-слой мог
// принимать поддельную реализацию в тестах, не поднимая настоящий SMTP-сервер.
type Sender interface {
	Send(to, subject, body string) error
}

// SMTPSender шлёт простые текстовые письма через внешний релей.
type SMTPSender struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

// Send отправляет письмо в виде простого текста. net/smtp.SendMail сам
// переключается на STARTTLS, если сервер объявляет это расширение при HELO,
// поэтому TLS здесь настраивать не нужно.
func (s *SMTPSender) Send(to, subject, body string) error {
	addr := s.Host + ":" + s.Port

	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	}

	return smtp.SendMail(addr, auth, s.From, []string{to}, buildMessage(s.From, to, subject, body))
}

// buildMessage собирает письмо в виде, пригодном для net/smtp.SendMail —
// он ожидает готовые RFC 5322-заголовки, включая завершающую CRLF-пустую
// строку перед телом. Тема кодируется по RFC 2047: она на русском языке, а
// заголовки допускают только ASCII.
func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", subject))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)

	return []byte(b.String())
}
