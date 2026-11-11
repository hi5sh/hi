package cli

// Email from hi (docs/specs/approved/hi_people.md): sign-in links and
// codes, and invitations, nothing else. hi speaks SMTP only, which every
// sender has: an organisation's relay, Google Workspace's, Microsoft
// 365's, Postmark, SES, Resend. The machine keeps one setting, in
// mail.json beside the apps' links (mode 600), which the admin page and
// every app's service read.

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	mailSecuritySTARTTLS = "starttls" // port 587: plain, then TLS before anything is said
	mailSecurityTLS      = "tls"      // port 465: TLS from the start
	mailSecurityNone     = "none"     // a relay on this machine or the network, no TLS
)

// mailSettings is how the machine sends email.
type mailSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	From     string `json:"from"`           // the address it comes from
	Name     string `json:"name,omitempty"` // the name it comes from
}

func mailSettingsPath() string { return filepath.Join(appsDir(), "mail.json") }

func loadMailSettings() (mailSettings, bool) {
	var settings mailSettings
	data, err := os.ReadFile(mailSettingsPath())
	if err != nil || json.Unmarshal(data, &settings) != nil || settings.Host == "" || settings.From == "" {
		return mailSettings{}, false
	}
	return settings, true
}

func saveMailSettings(settings mailSettings) error {
	data, _ := json.MarshalIndent(settings, "", "  ")
	os.MkdirAll(appsDir(), 0o700)
	return writeFileAtomic(mailSettingsPath(), append(data, '\n'), 0o600)
}

// check cleans the settings, and says what's wrong with them.
func (s mailSettings) check() (mailSettings, error) {
	s.Host = strings.ToLower(strings.TrimSpace(s.Host))
	s.Security = firstNonEmpty(strings.ToLower(strings.TrimSpace(s.Security)), mailSecuritySTARTTLS)
	s.Username = strings.TrimSpace(s.Username)
	s.Name = strings.TrimSpace(s.Name)
	if s.Host == "" || strings.ContainsAny(s.Host, " /:@\r\n") {
		return s, fmt.Errorf("the SMTP server is a host name, such as smtp.corp.example, not %q", s.Host)
	}
	switch s.Security {
	case mailSecuritySTARTTLS, mailSecurityTLS, mailSecurityNone:
	default:
		return s, fmt.Errorf("security is starttls, tls, or none, not %q", s.Security)
	}
	if s.Port == 0 {
		s.Port = map[string]int{mailSecuritySTARTTLS: 587, mailSecurityTLS: 465, mailSecurityNone: 25}[s.Security]
	}
	if s.Port < 1 || s.Port > 65535 {
		return s, fmt.Errorf("the port is a number, such as 587, not %d", s.Port)
	}
	from, err := appCheckEmail(s.From)
	if err != nil {
		return s, fmt.Errorf("the address email comes from: %w", err)
	}
	s.From = from
	if len(s.Name) > 80 || strings.ContainsAny(s.Name, "\r\n") {
		return s, errors.New("the name email comes from is a line, up to 80 letters")
	}
	if strings.ContainsAny(s.Username+s.Password, "\r\n") {
		return s, errors.New("the user name and password are one line each")
	}
	if s.Security == mailSecurityNone && s.Password != "" && !mailLocalHost(s.Host) {
		return s, errors.New("a password is only sent over TLS: security starttls or tls, or a relay on this machine")
	}
	return s, nil
}

// mailLocalHost is a host on this machine, where a password may go
// without TLS.
func mailLocalHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// mailMessage is one email.
type mailMessage struct {
	To      string
	Subject string
	Text    string
}

// mailErrOff is the machine without an email setting.
var mailErrOff = errors.New("this machine doesn't send email yet: an admin sets it up on the admin page (Keys, Email), or with hi admin mail set")

// mailSend sends one email; tests replace it.
var mailSend = smtpSend

// sendMail sends an email with the machine's setting.
func sendMail(message mailMessage) error {
	settings, ok := loadMailSettings()
	if !ok {
		return mailErrOff
	}
	return mailSend(settings, message)
}

// mailFormat is a message as SMTP carries it: plain text, quoted-
// printable, with nothing that tracks who opened it.
func mailFormat(settings mailSettings, message mailMessage, now time.Time) ([]byte, error) {
	to, err := appCheckEmail(message.To)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(message.Subject, "\r\n") {
		return nil, errors.New("a subject is one line")
	}
	from := (&mail.Address{Name: settings.Name, Address: settings.From}).String()
	_, domain, _ := strings.Cut(settings.From, "@")
	id := make([]byte, 12)
	rand.Read(id)
	var b bytes.Buffer
	for _, header := range [][2]string{
		{"From", from},
		{"To", (&mail.Address{Address: to}).String()},
		{"Subject", mime.QEncoding.Encode("utf-8", message.Subject)},
		{"Date", now.Format(time.RFC1123Z)},
		{"Message-ID", "<" + hex.EncodeToString(id) + "@" + domain + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=utf-8"},
		{"Content-Transfer-Encoding", "quoted-printable"},
		// An automatic email: no out-of-office answers to it.
		{"Auto-Submitted", "auto-generated"},
		{"X-Auto-Response-Suppress", "All"},
	} {
		b.WriteString(header[0] + ": " + header[1] + "\r\n")
	}
	b.WriteString("\r\n")
	body := quotedprintable.NewWriter(&b)
	body.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(message.Text, "\r\n", "\n"), "\n", "\r\n")))
	body.Close()
	b.WriteString("\r\n")
	return b.Bytes(), nil
}

// smtpSend speaks SMTP to the machine's server.
func smtpSend(settings mailSettings, message mailMessage) error {
	data, err := mailFormat(settings, message, time.Now())
	if err != nil {
		return err
	}
	address := net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port))
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	if settings.Security == mailSecurityTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: settings.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return fmt.Errorf("the SMTP server %s didn't answer: %w", address, err)
	}
	conn.SetDeadline(time.Now().Add(time.Minute))
	client, err := smtp.NewClient(conn, settings.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("the SMTP server %s: %w", address, err)
	}
	defer client.Close()
	hostname, _ := os.Hostname()
	if err := client.Hello(firstNonEmpty(hostname, "localhost")); err != nil {
		return fmt.Errorf("the SMTP server %s refused hello: %w", address, err)
	}
	if settings.Security == mailSecuritySTARTTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("the SMTP server %s doesn't offer STARTTLS; set security tls for port 465, or none for a relay that has no TLS", address)
		}
		if err := client.StartTLS(&tls.Config{ServerName: settings.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("TLS with the SMTP server %s: %w", address, err)
		}
	}
	if settings.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", settings.Username, settings.Password, settings.Host)); err != nil {
			return fmt.Errorf("the SMTP server %s refused the user name and password: %w", address, err)
		}
	}
	if err := client.Mail(settings.From); err != nil {
		return fmt.Errorf("the SMTP server %s refused to send from %s: %w", address, settings.From, err)
	}
	if err := client.Rcpt(message.To); err != nil {
		return fmt.Errorf("the SMTP server %s refused to send to %s: %w", address, message.To, err)
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("the SMTP server %s didn't take the email: %w", address, err)
	}
	return client.Quit()
}

// ---------------------------------------------------------------------------
// hi admin mail

const mailUsage = `usage: hi admin mail                      how the machine sends email
       hi admin mail set --host <host> --from <address> [--port <port>]
           [--security starttls|tls|none] [--user <name>] [--name <from name>]
                                          the password, if any, on standard input
       hi admin mail test <address>       send a test email
       hi admin mail off                  send none`

func adminMailCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		settings, ok := loadMailSettings()
		if !ok {
			fmt.Fprintln(stdout, "The machine sends no email. hi admin mail set --host smtp.corp.example --from hi@corp.example --user hi < password.txt")
			return nil
		}
		fmt.Fprintf(stdout, "Email comes from %s, through %s:%d (%s)%s.\n", (&mail.Address{Name: settings.Name, Address: settings.From}).String(), settings.Host, settings.Port, settings.Security,
			map[bool]string{true: ", signed in as " + settings.Username, false: ""}[settings.Username != ""])
		return nil
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, mailUsage)
		return nil
	case "set":
		var settings mailSettings
		for i := 1; i < len(args); i++ {
			if i+1 >= len(args) {
				return usageError{mailUsage}
			}
			value := args[i+1]
			switch args[i] {
			case "--host":
				settings.Host = value
			case "--port":
				port, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("the port is a number, such as 587, not %q", value)
				}
				settings.Port = port
			case "--security":
				settings.Security = value
			case "--user":
				settings.Username = value
			case "--from":
				settings.From = value
			case "--name":
				settings.Name = value
			default:
				return usageError{mailUsage}
			}
			i++
		}
		if settings.Username != "" {
			password, err := readSecretLine(stdin, stdout, "The SMTP password (not shown)")
			if err != nil {
				return err
			}
			settings.Password = password
		}
		return adminMailSet(settings, adminActor(), stdout)
	case "test":
		if len(args) != 2 {
			return usageError{mailUsage}
		}
		if err := adminMailTest(args[1]); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Sent to %s. If it doesn't arrive, look in spam: the From domain needs SPF and DKIM for this server.\n", args[1])
		return nil
	case "off":
		if err := os.Remove(mailSettingsPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		adminRecord(adminActor(), "", "turned email off")
		fmt.Fprintln(stdout, "The machine sends no email now: people sign in with Slack links.")
		return nil
	}
	return usageError{mailUsage}
}

// adminMailSet keeps new settings, keeping the old password when none is
// given for the same user.
func adminMailSet(settings mailSettings, by string, stdout io.Writer) error {
	if old, ok := loadMailSettings(); ok && settings.Password == "" && settings.Username != "" && settings.Username == old.Username && settings.Host == old.Host {
		settings.Password = old.Password
	}
	settings, err := settings.check()
	if err != nil {
		return err
	}
	if err := saveMailSettings(settings); err != nil {
		return err
	}
	adminRecord(by, "", "set how the machine sends email: from "+settings.From+" through "+settings.Host)
	if stdout != nil {
		fmt.Fprintf(stdout, "Email comes from %s through %s:%d. hi admin mail test <your address> checks it.\n", settings.From, settings.Host, settings.Port)
	}
	return nil
}

// adminMailTest sends a test email.
func adminMailTest(to string) error {
	to, err := appCheckEmail(to)
	if err != nil {
		return err
	}
	return sendMail(mailMessage{To: to, Subject: "A test from hi on " + adminMachineName(),
		Text: "This is a test from hi on " + adminMachineName() + ". The machine can send email: sign-in links and invitations reach people.\n\n" +
			"If this landed in spam, ask your IT team for SPF and DKIM records that let this server send for the From domain.\n"})
}

// readSecretLine reads one line, a secret, without echoing it in a
// terminal.
func readSecretLine(stdin io.Reader, stdout io.Writer, prompt string) (string, error) {
	if file, ok := stdin.(*os.File); ok && isTerminal(file) {
		fmt.Fprint(stdout, prompt+": ")
		secret, err := readSecret(file, stdout)
		fmt.Fprintln(stdout)
		return strings.TrimSpace(string(secret)), err
	}
	data, err := io.ReadAll(io.LimitReader(stdin, 4096))
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimSpace(line), nil
}
