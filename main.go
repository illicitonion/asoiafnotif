package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/smtp"
	"strconv"
	"time"

	"golang.org/x/net/html"
	"gopkg.in/xmlpath.v2"
)

var (
	verbose = flag.Bool("v", false, "Whether to log verbosely")

	lastFile        = flag.String("file", "", "File to store count between invocations")
	ipssessionfront = flag.String("ipssessionfront", "", "ipssessionfront cookie")
	memberid        = flag.String("memberid", "", "memberid cookie")
	cfduid          = flag.String("cfduid", "", "cfduid cookie")
	passhash        = flag.String("passhash", "", "passhash cookie")
	notifyEmail     = flag.String("notify_email", "", "Email address to notify")

	smtpServer   = flag.String("smtp_server", "", "SMTP server")
	smtpUser     = flag.String("smtp_user", "", "SMTP user")
	smtpPassword = flag.String("smtp_password", "", "SMTP password")
)

func main() {
	flag.Parse()
	if *lastFile == "" {
		log.Fatal("Need to specify --file")
	}
	if *smtpServer == "" {
		log.Fatal("Need to specify --smtp_server")
	}
	if *smtpUser == "" {
		log.Fatal("Need to specify --smtp_user")
	}
	if *smtpPassword == "" {
		log.Fatal("Need to specify --smtp_password")
	}
	if *ipssessionfront == "" {
		log.Fatal("Need to specify --ipsessionfront")
	}
	if *memberid == "" {
		log.Fatal("Need to specify --memberid")
	}
	if *cfduid == "" {
		log.Fatal("Need to specify --cfduid")
	}
	if *passhash == "" {
		log.Fatal("Need to specify --passhash")
	}
	if *notifyEmail == "" {
		log.Fatal("Need to specify --notify_email")
	}

	e := &emailer{
		from:         *smtpUser,
		smtpServer:   *smtpServer,
		smtpPassword: *smtpPassword,
	}

	checkAndNotify(*ipssessionfront, *memberid, *cfduid, *passhash, *notifyEmail, e)
}

func checkAndNotify(ipssessionfront, memberid, cfduid, passhash, notifyEmail string, e *emailer) {
	notifications, err := getNotifications(ipssessionfront, memberid, cfduid, passhash)
	if err != nil {
		if emailErr := e.email(notifyEmail, "Error scraping notifications", err.Error()); emailErr != nil {
			log.Fatal(err)
		}
	}
	bs, err := ioutil.ReadFile(*lastFile)
	lastNotifications, convErr := strconv.Atoi(string(bs))
	if err != nil || convErr != nil {
		log.Print("Error reading last file, falling back to 0:", err, convErr)
		lastNotifications = 0
	}
	ioutil.WriteFile(*lastFile, []byte(strconv.Itoa(notifications)), 0700)
	if notifications <= lastNotifications {
		if *verbose {
			log.Print("Skipping email beacuse already notified for this number of notifications")
		}
		return
	}
	if *verbose {
		log.Printf("Emailing for %d notification(s)", notifications)
	}
	if notifications > 0 {
		if emailErr := e.email(notifyEmail, "ASOIAF notifications", fmt.Sprintf("You have %d new notification(s)! Go check https://asoiaf.westeros.org/", notifications)); emailErr != nil {
			log.Fatal(err)
		}
	}
}

func getNotifications(ipssessionfront, memberid, cfduid, passhash string) (int, error) {
	var client http.Client
	req, _ := http.NewRequest("GET", "https://asoiaf.westeros.org/index.php", nil)
	req.AddCookie(&http.Cookie{
		Name:  "ips4_IPSSessionFront",
		Value: ipssessionfront,
	})
	req.AddCookie(&http.Cookie{
		Name:  "ips4_member_id",
		Value: memberid,
	})
	req.AddCookie(&http.Cookie{
		Name:  "__cfduid",
		Value: cfduid,
	})
	req.AddCookie(&http.Cookie{
		Name:  "ips4_pass_hash",
		Value: passhash,
	})
	resp, err := retryHTTPDo(client, req)
	if err != nil {
		return 0, fmt.Errorf("error making HTTP request: %v", err)
	}
	if resp.StatusCode != 200 {
		errBs, _ := httputil.DumpResponse(resp, true)
		log.Println(string(errBs))
		return 0, fmt.Errorf("HTTP response code %d", resp.StatusCode)
	}

	root, err := html.Parse(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("error parsing HTML: %v", err)
	}

	var b bytes.Buffer
	html.Render(&b, root)
	xmlRoot, err := xmlpath.ParseHTML(&b)
	if err != nil {
		return 0, fmt.Errorf("error parsing fixed HTML: %v", err)
	}

	path := xmlpath.MustCompile(`//node()[contains(@class, 'ipsNotificationCount') and @data-notificationtype='total']`)
	if contents, ok := path.String(xmlRoot); ok {
		return strconv.Atoi(contents)
	}
	paths := xmlpath.MustCompile(`//node()[contains(@class, 'ipsNotificationCount')]`)
	notifications := 0
	iter := paths.Iter(xmlRoot)
	for iter.Next() {
		contents := iter.Node().String()
		this, _ := strconv.Atoi(contents)
		notifications += this
	}
	return notifications, nil
}

func retryHTTPDo(client http.Client, req *http.Request) (resp *http.Response, err error) {
	for i := 0; i < 5; i++ {
		resp, err = client.Do(req)
		if err != nil {
			log.Println("Error making HTTP request, maybe retrying: %v", err.Error())
		} else if resp.StatusCode != 200 {
			errBs, _ := httputil.DumpResponse(resp, true)
			log.Println("Bad response to HTTP request, maybe retrying: %v", string(errBs))
		} else {
			return
		}
		time.Sleep(5 * time.Second)
	}
	return
}

type emailer struct {
	from         string
	smtpServer   string
	smtpPassword string
	smtpPort     int
}

func (e *emailer) email(to, subject, body string) error {
	auth := smtp.PlainAuth(
		"",
		e.from,
		e.smtpPassword,
		e.smtpServer,
	)

	port := e.smtpPort
	if port == 0 {
		port = 25
	}

	return smtp.SendMail(
		fmt.Sprintf("%s:%d", e.smtpServer, port),
		auth,
		e.from,
		[]string{to},
		[]byte(
			`To: `+to+"\r\n"+
				`From: ASOIAF notifications <`+e.from+`>`+"\r\n"+
				`Subject: `+subject+"\r\n\r\n"+
				body+"\r\n",
		),
	)
}
