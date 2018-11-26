package bdiscord

import (
	"github.com/Subluminal/matterbridge/bridge/config"
	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	"regexp"
	"strings"
	"sync"
)

type Attachment struct {
    id           string
    form         string
}

type Message struct {
    id           string
    content      string
    attachments  []Attachment
}

type bdiscord struct {
	c              *discordgo.Session
	Config         *config.Protocol
	Remote         chan config.Message
	Account        string
	Channels       []*discordgo.Channel
	Nick           string
	UseChannelID   bool
    MessageHist    []Message
	userMemberMap  map[string]*discordgo.Member
	guildID        string
	webhookID      string
	webhookToken   string
	channelInfoMap map[string]*config.ChannelInfo
	sync.RWMutex
}

var flog *log.Entry
var protocol = "discord"
var mentionRegex = regexp.MustCompile(`<@!?(\d+)>`)
var emojiRegex = regexp.MustCompile(`<(:\w+:)\d+>`)
var pingRegex = regexp.MustCompile(`@\w+`)

func init() {
    flog = log.WithFields(log.Fields{"module": protocol})
}

func New(cfg config.Protocol, account string, c chan config.Message) *bdiscord {
	b := &bdiscord{}
	b.Config = &cfg
	b.Remote = c
	b.Account = account
    b.MessageHist = make([]Message, 64)
	b.userMemberMap = make(map[string]*discordgo.Member)
	b.channelInfoMap = make(map[string]*config.ChannelInfo)
	if b.Config.WebhookURL != "" {
		flog.Debug("Configuring Discord Incoming Webhook")
		b.webhookID, b.webhookToken = b.splitURL(b.Config.WebhookURL)
	}
	return b
}

func (b *bdiscord) Connect() error {
	var err error
	flog.Info("Connecting")
	if b.Config.WebhookURL == "" {
		flog.Info("Connecting using token")
	} else {
		flog.Info("Connecting using webhookurl (for posting) and token")
	}
	if !strings.HasPrefix(b.Config.Token, "Bot ") {
		b.Config.Token = "Bot " + b.Config.Token
	}
	b.c, err = discordgo.New(b.Config.Token)
	if err != nil {
		flog.Debugf("%#v", err)
		return err
	}
	flog.Info("Connection succeeded")
	b.c.AddHandler(b.messageCreate)
	b.c.AddHandler(b.memberUpdate)
	b.c.AddHandler(b.messageUpdate)
	b.c.AddHandler(b.messageDelete)
	err = b.c.Open()
	if err != nil {
		flog.Debugf("%#v", err)
		return err
	}
	guilds, err := b.c.UserGuilds(100, "", "")
	if err != nil {
		flog.Debugf("%#v", err)
		return err
	}
	userinfo, err := b.c.User("@me")
	if err != nil {
		flog.Debugf("%#v", err)
		return err
	}
	b.Nick = userinfo.Username
	for _, guild := range guilds {
		if guild.Name == b.Config.Server {
			b.Channels, err = b.c.GuildChannels(guild.ID)
			b.guildID = guild.ID
			if err != nil {
				flog.Debugf("%#v", err)
				return err
			}
		}
	}
	return nil
}

func (b *bdiscord) Disconnect() error {
	return nil
}

func (b *bdiscord) JoinChannel(channel config.ChannelInfo) error {
	b.channelInfoMap[channel.ID] = &channel
	idcheck := strings.Split(channel.Name, "ID:")
	if len(idcheck) > 1 {
		b.UseChannelID = true
	}
	return nil
}

func (b *bdiscord) Send(msg config.Message) (string, error) {
	flog.Debugf("Receiving %#v", msg)
	channelID := b.getChannelID(msg.Channel)
	if channelID == "" {
		flog.Errorf("Could not find channelID for %v", msg.Channel)
		return "", nil
	}
	var re = regexp.MustCompile(`@(here|everyone)`)
	msg.Text = re.ReplaceAllString(msg.Text, "")
	if msg.Event == config.EVENT_USER_ACTION {
		msg.Text = "_" + msg.Text + "_"
	}
    /*
    guilds := map[string]struct{}{}
    for _, ch := range b.Channels {
        guilds[ch.GuildID] = struct{}{}
    }
    nickMap := map[string]string{}
    for guildid := range guilds {
        members, err := b.c.GuildMembers(guildid, 0, 1000)
        if err != nil {
            continue
        }
        for _, member := range members {
            hl := "<@"+member.User.ID+">"
            nickMap[strings.ToLower(member.User.Username)] = hl
            if member.Nick != "" {
                nickMap[strings.ToLower(member.Nick)] = hl
            }
        }
    }
    text := pingRegex.ReplaceAllStringFunc(msg.Text, func (match string) string {
        flog.Debugf("Searching for %s", match)
        nick := strings.ToLower(match[1:])
        if val, ok := nickMap[nick]; ok {
            flog.Debugf("Found %s", val)
            return val
        }
        return match
    })
    */

	wID := b.webhookID
	wToken := b.webhookToken
	if ci, ok := b.channelInfoMap[msg.Channel+b.Account]; ok {
		if ci.Options.WebhookURL != "" {
			wID, wToken = b.splitURL(ci.Options.WebhookURL)
		}
	}

	if wID == "" {
		flog.Debugf("Broadcasting using token (API)")
		if msg.Event == config.EVENT_MSG_DELETE {
			if msg.ID == "" {
				return "", nil
			}
			err := b.c.ChannelMessageDelete(channelID, msg.ID)
			return "", err
		}
		if msg.ID != "" {
			_, err := b.c.ChannelMessageEdit(channelID, msg.ID, msg.Username+msg.Text)
			return msg.ID, err
		}
		res, err := b.c.ChannelMessageSend(channelID, msg.Username+msg.Text)
		if err != nil {
			return "", err
		}
		return res.ID, err
	}
	flog.Debugf("Broadcasting using Webhook")
	err := b.c.WebhookExecute(
		wID,
		wToken,
		true,
		&discordgo.WebhookParams{
			Content:   msg.Text,
			Username:  msg.Username,
			AvatarURL: msg.Avatar,
		})
	return "", err
}

func (b *bdiscord) findNick(s *discordgo.Session, user *discordgo.User) string {
    name := user.Username
    for _, ch := range b.Channels {
        member, err := s.GuildMember(ch.GuildID, user.ID)
        if err == nil && member.Nick != "" {
            name = member.Nick
            break
        }
    }
    return "@"+name
}

func (b *bdiscord) CleanContent(content string) string {
    guilds := map[string]struct{}{}
    for _, ch := range b.Channels {
        guilds[ch.GuildID] = struct{}{}
    }
    nickMap := map[string]string{}
    for guildid := range guilds {
        flog.Debugf("Working with guild %d", guildid)
        members, err := b.c.GuildMembers(guildid, "0", 1000)
        if err != nil {
            continue
        }
        for _, member := range members {
            nickMap[member.User.ID] = "@"+member.User.Username
            if member.Nick != "" {
                nickMap[member.User.ID] = "@"+member.Nick
            }
            flog.Debugf("%d -> %s", member.User.ID, nickMap[member.User.ID])
        }
    }
    flog.Debugf("Nick map: %#v", nickMap)
    text := mentionRegex.ReplaceAllStringFunc(content, func (match string) string {
        id := mentionRegex.FindStringSubmatch(match)[1]
        flog.Debugf("Searching for %s", id)
        if val, ok := nickMap[id]; ok {
            flog.Debugf("Found %s", val)
            return val
        }
        return match
    })
    return emojiRegex.ReplaceAllStringFunc(text, func (match string) string {
        return emojiRegex.FindStringSubmatch(match)[1]
    })
}

func (b *bdiscord) messageDelete(s *discordgo.Session, m *discordgo.MessageDelete) {
	rmsg := config.Message{Account: b.Account, ID: m.ID, Event: config.EVENT_MSG_DELETE, Text: config.EVENT_MSG_DELETE}
	rmsg.Channel = b.getChannelName(m.ChannelID)
	if b.UseChannelID {
		rmsg.Channel = "ID:" + m.ChannelID
	}
	flog.Debugf("Sending message from %s to gateway", b.Account)
	flog.Debugf("Message is %#v", rmsg)
	b.Remote <- rmsg
}

func (b *bdiscord) messageUpdate(s *discordgo.Session, m *discordgo.MessageUpdate) {
	if b.Config.EditDisable {
		return
	}
	// only when message is actually edited
	if m.Message.EditedTimestamp != "" {
		flog.Debugf("Sending edit message")
		m.Content = m.Content + b.Config.EditSuffix
		b.messageCreate(s, (*discordgo.MessageCreate)(m))
	}
}

func (b *bdiscord) messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// not relay our own messages
	if m.Author.Username == b.Nick {
		return
	}
	// if using webhooks, do not relay if it's ours
	if b.useWebhook() && m.Author.Bot && b.isWebhookID(m.Author.ID) {
		return
	}

	if len(m.Attachments) > 0 {
		for _, attach := range m.Attachments {
			m.Content = m.Content + "\n" + attach.URL
		}
	}

	var text string
	if m.Content != "" {
		flog.Debugf("Receiving message %#v", m.Message)
		if len(m.MentionRoles) > 0 {
			m.Message.Content = b.replaceRoleMentions(m.Message.Content)
		}
		m.Message.Content = b.stripCustomoji(m.Message.Content)
		m.Message.Content = b.replaceChannelMentions(m.Message.Content)
		text = m.ContentWithMentionsReplaced()
	}

	rmsg := config.Message{Account: b.Account, Avatar: "https://cdn.discordapp.com/avatars/" + m.Author.ID + "/" + m.Author.Avatar + ".jpg",
		UserID: m.Author.ID, ID: m.ID}

	rmsg.Channel = b.getChannelName(m.ChannelID)
	if b.UseChannelID {
		rmsg.Channel = "ID:" + m.ChannelID
	}

	if !b.Config.UseUserName {
		rmsg.Username = b.getNick(m.Author)
	} else {
		rmsg.Username = m.Author.Username
	}

	if b.Config.ShowEmbeds && m.Message.Embeds != nil {
		for _, embed := range m.Message.Embeds {
			text = text + "embed: " + embed.Title + " - " + embed.Description + " - " + embed.URL + "\n"
		}
	}

	// no empty messages
	if text == "" {
		return
	}

	text, ok := b.replaceAction(text)
	if ok {
		rmsg.Event = config.EVENT_USER_ACTION
	}

	rmsg.Text = text
	flog.Debugf("Sending message from %s on %s to gateway", m.Author.Username, b.Account)
	flog.Debugf("Message is %#v", rmsg)
	b.Remote <- rmsg
}

func (b *bdiscord) memberUpdate(s *discordgo.Session, m *discordgo.GuildMemberUpdate) {
	b.Lock()
	if _, ok := b.userMemberMap[m.Member.User.ID]; ok {
		flog.Debugf("%s: memberupdate: user %s (nick %s) changes nick to %s", b.Account, m.Member.User.Username, b.userMemberMap[m.Member.User.ID].Nick, m.Member.Nick)
	}
	b.userMemberMap[m.Member.User.ID] = m.Member
	b.Unlock()
}

func (b *bdiscord) getNick(user *discordgo.User) string {
	var err error
	b.Lock()
	defer b.Unlock()
	if _, ok := b.userMemberMap[user.ID]; ok {
		if b.userMemberMap[user.ID] != nil {
			if b.userMemberMap[user.ID].Nick != "" {
				// only return if nick is set
				return b.userMemberMap[user.ID].Nick
			}
			// otherwise return username
			return user.Username
		}
	}
	// if we didn't find nick, search for it
	member, err := b.c.GuildMember(b.guildID, user.ID)
	if err != nil {
		return user.Username
	}
	b.userMemberMap[user.ID] = member
	// only return if nick is set
	if b.userMemberMap[user.ID].Nick != "" {
		return b.userMemberMap[user.ID].Nick
	}
	return user.Username
}

func (b *bdiscord) getChannelID(name string) string {
    idcheck := strings.Split(name, "ID:")
    if len(idcheck) > 1 {
        return idcheck[1]
    }
    for _, channel := range b.Channels {
        if channel.Name == name {
            return channel.ID
        }
    }
    return ""
}

func (b *bdiscord) getChannelName(id string) string {
    for _, channel := range b.Channels {
        if channel.ID == id {
            return channel.Name
        }
    }
    return ""
}

func (b *bdiscord) replaceRoleMentions(text string) string {
	roles, err := b.c.GuildRoles(b.guildID)
	if err != nil {
		flog.Debugf("%#v", string(err.(*discordgo.RESTError).ResponseBody))
		return text
	}
	for _, role := range roles {
		text = strings.Replace(text, "<@&"+role.ID+">", "@"+role.Name, -1)
	}
	return text
}

func (b *bdiscord) replaceChannelMentions(text string) string {
	var err error
	re := regexp.MustCompile("<#[0-9]+>")
	text = re.ReplaceAllStringFunc(text, func(m string) string {
		channel := b.getChannelName(m[2 : len(m)-1])
		// if at first don't succeed, try again
		if channel == "" {
			b.Channels, err = b.c.GuildChannels(b.guildID)
			if err != nil {
				return "#unknownchannel"
			}
			channel = b.getChannelName(m[2 : len(m)-1])
			return "#" + channel
		}
		return "#" + channel
	})
	return text
}

func (b *bdiscord) replaceAction(text string) (string, bool) {
	if strings.HasPrefix(text, "_") && strings.HasSuffix(text, "_") {
		return strings.Replace(text, "_", "", -1), true
	}
	return text, false
}

func (b *bdiscord) stripCustomoji(text string) string {
	// <:doge:302803592035958784>
	re := regexp.MustCompile("<(:.*?:)[0-9]+>")
	return re.ReplaceAllString(text, `$1`)
}

// splitURL splits a webhookURL and returns the id and token
func (b *bdiscord) splitURL(url string) (string, string) {
	webhookURLSplit := strings.Split(url, "/")
	return webhookURLSplit[len(webhookURLSplit)-2], webhookURLSplit[len(webhookURLSplit)-1]
}

// useWebhook returns true if we have a webhook defined somewhere
func (b *bdiscord) useWebhook() bool {
	if b.Config.WebhookURL != "" {
		return true
	}
	for _, channel := range b.channelInfoMap {
		if channel.Options.WebhookURL != "" {
			return true
		}
	}
	return false
}

// isWebhookID returns true if the specified id is used in a defined webhook
func (b *bdiscord) isWebhookID(id string) bool {
	if b.Config.WebhookURL != "" {
		wID, _ := b.splitURL(b.Config.WebhookURL)
		if wID == id {
			return true
		}
	}
	for _, channel := range b.channelInfoMap {
		if channel.Options.WebhookURL != "" {
			wID, _ := b.splitURL(channel.Options.WebhookURL)
			if wID == id {
				return true
			}
		}
	}
	return false
}
