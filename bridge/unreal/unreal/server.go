package unreal

import (
    "regexp"
    "crypto/tls"
    "bufio"
    "bytes"
    "github.com/42wim/matterbridge/bridge/config"
    log "github.com/Sirupsen/logrus"
    ircm "github.com/sorcix/irc"
    "strings"
)

type IrcCallback func(nick string, channel string, text string)

type Server struct {
    conn    *tls.Conn
    names   map[string]map[string]string
    queue   chan string
    nick    string
    name    string
    sid     string
    pass    string
    cb      IrcCallback
    ReadQ   chan config.Message
}

func New(name string, sid string, pass string, nick string, callback IrcCallback) *Server {
    s := &Server{}
    s.nick = nick
    s.name = name
    s.sid = sid
    s.pass = pass
    s.names = make(map[string]map[string]string)
    s.cb = callback
    return s
}

func (s *Server) Connect(addr string) error {
    conf := &tls.Config{
        InsecureSkipVerify: true,
    }

    conn, err := tls.Dial("tcp", addr, conf)
    if err != nil {
        log.Println(err)
        return err
    }
    s.conn = conn
    go s.readLoop()
    s.register()
    return nil
}

func (s *Server) Join(channel string) error {
    s.names[channel] = make(map[string]string)
    s.sendRaw(":" + s.nick + " JOIN :" + channel)
    return nil
}

func (s *Server) Send(channel string, name string, msg string, account string) error {
    nick := ""
    if val, ok := s.names[channel][name]; ok {
        nick = val
    } else {
        nick = linearize(name, account)
        s.names[channel][name] = nick
        user := strings.Split(account, ".")[0]
        s.sendRaw(":" + s.sid + " UID " + nick + " 1 0 " + user + " localhost * 0 +ixw bridge.matterbridge * :Matterbridge Virtual User")
        s.sendRaw(":" + nick + " JOIN :" + channel)
    }
    log.Printf("Fake sending message to %s from %s as %s", channel, name, nick)
    s.sendRaw(":" + nick + " PRIVMSG " + channel + " :" + msg)
    return nil
}

func linearize(name string, account string) string {
    charrgx := regexp.MustCompile(`[^A-Za-z0-9_\-\[\]\\^{}|]`)
    name = charrgx.ReplaceAllString(name, "")
    startrgx := regexp.MustCompile(`\A[0-9]+`)
    name = startrgx.ReplaceAllString(name, "")
    if len(name) > 14 {
        name = name[0:14] + "/" + account[0:1]
    } else {
        name = name + "/" + account[0:1]
    }
    return name
}

func (s *Server) sendRaw(line string) {
    packet := []byte(line + "\r\n")
    log.Println("Sending |= " + line)
    s.conn.Write(packet)
}

func (s *Server) register() {
    s.sendRaw("PASS :" + s.pass)
    s.sendRaw("PROTOCTL EAUTH=" + s.name + " SID=" + s.sid)
    s.sendRaw("SERVER " + s.name + " 1 :Matterbridge")
    s.sendRaw("UID " + s.nick + " 1 0 bridge localhost * 0 +ixw bridge.matterbridge * :Matterbridge")
    s.sendRaw("EOS")
}

func (s *Server) readLoop() {
    reader := bufio.NewReader(s.conn)
	scanner := bufio.NewScanner(reader)
	scanner.Split(scanCRLF)
    for scanner.Scan() {
        line := scanner.Text()
		msg := ircm.ParseMessage(line)
        s.handle(msg)
    }
}

func (s *Server) handle(msg *ircm.Message) {
    switch msg.Command {
    case "PING":
        log.Println("Ping/Pong |= " + string(msg.Bytes()))
        s.sendRaw("PONG :" + msg.Trailing)
    case "EOS":
        log.Println("EOS |= " + string(msg.Bytes()))
    case "KILL":
        nick := msg.Params[0]
        log.Println("KILL: " + nick)
        for c, nickMap := range s.names {
            for k, v := range nickMap {
                if v == nick {
                    delete(s.names[c], k)
                }
            }
        }
    case "PRIVMSG":
        channel := msg.Params[0]
        text := msg.Trailing
        nick := msg.Prefix.Name
        if nickMap, ok := s.names[channel]; ok {
            pingRegex := regexp.MustCompile(`[A-Za-z0-9_\-\[\]\\^{}|]+/[A-Za-z]`)
            text = pingRegex.ReplaceAllStringFunc(text, func (match string) string {
                log.Debugf("Searching for %s", match)
                for k, v := range nickMap {
                    if match == v {
                        return k
                    }
                }
                return match
            })
            s.cb(nick, channel, text)
            log.Println(nick + " ! " + text)
        }
    default:
        log.Printf("Unhandled Command: %s |= %s", msg.Command, msg.Bytes())
    }
}

// dropCR drops a terminal \r from the data.
func dropCR(data []byte) []byte {
    if len(data) > 0 && data[len(data)-1] == '\r' {
        return data[0 : len(data)-1]
    }
    return data
}


func scanCRLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.Index(data, []byte{'\r','\n'}); i >= 0 {
		// We have a full newline-terminated line.
		return i + 2, dropCR(data[0:i]), nil
	}
	// If we're at EOF, we have a final, non-terminated line. Return it.
	if atEOF {
		return len(data), dropCR(data), nil
	}
	// Request more data.
	return 0, nil, nil
}
