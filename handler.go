package main

import (
	"log"
	"net"
	"time"

	"github.com/miekg/dns"
)

const (
	notIPQuery = 0
	_IP4Query  = 4
	_IP6Query  = 6
)

// Question type
type Question struct {
	Qname  string `json:"name"`
	Qtype  string `json:"type"`
	Qclass string `json:"class"`
}

// QuestionCacheEntry represents a full query from a client with metadata
type QuestionCacheEntry struct {
	Date    int64    `json:"date"`
	Remote  string   `json:"client"`
	Blocked bool     `json:"blocked"`
	Query   Question `json:"query"`
}

// String formats a question
func (q *Question) String() string {
	return q.Qname + " " + q.Qclass + " " + q.Qtype
}

// DNSHandler type
type DNSHandler struct {
	resolver *Resolver
	cache    Cache
	negCache Cache
}

// NewHandler returns a new DNSHandler
func NewHandler() *DNSHandler {
	var (
		clientConfig *dns.ClientConfig
		resolver     *Resolver
		cache        Cache
		negCache     Cache
	)

	resolver = &Resolver{clientConfig}

	cache = &MemoryCache{
		Backend:  make(map[string]Mesg, Config.Maxcount),
		Expire:   time.Duration(Config.Expire) * time.Second,
		Maxcount: Config.Maxcount,
	}
	negCache = &MemoryCache{
		Backend:  make(map[string]Mesg),
		Expire:   time.Duration(Config.Expire) * time.Second / 2,
		Maxcount: Config.Maxcount,
	}

	return &DNSHandler{resolver, cache, negCache}
}

func (h *DNSHandler) do(Net string, w dns.ResponseWriter, req *dns.Msg) {
	defer w.Close()
	q := req.Question[0]
	Q := Question{UnFqdn(q.Name), dns.TypeToString[q.Qtype], dns.ClassToString[q.Qclass]}

	var remote net.IP
	if Net == "tcp" {
		remote = w.RemoteAddr().(*net.TCPAddr).IP
	} else {
		remote = w.RemoteAddr().(*net.UDPAddr).IP
	}

	if Config.LogLevel > 0 {
		log.Printf("%s lookup　%s\n", remote, Q.String())
	}

	IPQuery := h.isIPQuery(q)

	// Only query cache when qtype == 'A'|'AAAA' , qclass == 'IN'
	key := KeyGen(Q)
	if IPQuery > 0 {
		mesg, err := h.cache.Get(key)
		if err != nil {
			if mesg, err = h.negCache.Get(key); err != nil {
				if Config.LogLevel > 0 {
					log.Printf("%s didn't hit cache\n", Q.String())
				}
			} else {
				if Config.LogLevel > 0 {
					log.Printf("%s hit negative cache\n", Q.String())
				}
				dns.HandleFailed(w, req)
				return
			}
		} else {
			if Config.LogLevel > 0 {
				log.Printf("%s hit cache\n", Q.String())
			}

			// we need this copy against concurrent modification of Id
			msg := *mesg
			msg.Id = req.Id
			w.WriteMsg(&msg)
			return
		}
	}

	// Check blocklist
	if IPQuery > 0 {
		exists := BlockCache.Exists(Q.Qname)
		if exists {
			m := new(dns.Msg)
			m.SetReply(req)

			nullroute := net.ParseIP(Config.Nullroute)
			nullroutev6 := net.ParseIP(Config.Nullroutev6)

			switch IPQuery {
			case _IP4Query:
				rrHeader := dns.RR_Header{
					Name:   q.Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    Config.TTL,
				}
				a := &dns.A{Hdr: rrHeader, A: nullroute}
				m.Answer = append(m.Answer, a)
			case _IP6Query:
				rrHeader := dns.RR_Header{
					Name:   q.Name,
					Rrtype: dns.TypeAAAA,
					Class:  dns.ClassINET,
					Ttl:    Config.TTL,
				}
				a := &dns.AAAA{Hdr: rrHeader, AAAA: nullroutev6}
				m.Answer = append(m.Answer, a)
			}

			w.WriteMsg(m)

			if Config.LogLevel > 0 {
				log.Printf("%s found in blocklist\n", Q.Qname)
			}

			// log query
			NewEntry := QuestionCacheEntry{Date: time.Now().Unix(), Remote: remote.String(), Query: Q, Blocked: true}
			go QuestionCache.Add(NewEntry)

			// cache the block
			err := h.cache.Set(key, m)
			if err != nil {
				log.Printf("Set %s block cache failed: %s\n", Q.String(), err.Error())
			}

			return
		}
		if Config.LogLevel > 0 {
			log.Printf("%s not found in blocklist\n", Q.Qname)
		}
	}

	// log query
	NewEntry := QuestionCacheEntry{Date: time.Now().Unix(), Remote: remote.String(), Query: Q, Blocked: false}
	go QuestionCache.Add(NewEntry)

	mesg, err := h.resolver.Lookup(Net, req)

	if err != nil {
		log.Printf("resolve query error %s\n", err)
		dns.HandleFailed(w, req)

		// cache the failure, too!
		if err = h.negCache.Set(key, nil); err != nil {
			log.Printf("set %s negative cache failed: %v\n", Q.String(), err)
		}
		return
	}

	w.WriteMsg(mesg)

	if IPQuery > 0 && len(mesg.Answer) > 0 {
		err = h.cache.Set(key, mesg)
		if err != nil {
			log.Printf("set %s cache failed: %s\n", Q.String(), err.Error())
		}
		if Config.LogLevel > 0 {
			log.Printf("insert %s into cache\n", Q.String())
		}
	}
}

// DoTCP begins a tcp query
func (h *DNSHandler) DoTCP(w dns.ResponseWriter, req *dns.Msg) {
	go h.do("tcp", w, req)
}

// DoUDP begins a udp query
func (h *DNSHandler) DoUDP(w dns.ResponseWriter, req *dns.Msg) {
	go h.do("udp", w, req)
}

func (h *DNSHandler) isIPQuery(q dns.Question) int {
	if q.Qclass != dns.ClassINET {
		return notIPQuery
	}

	switch q.Qtype {
	case dns.TypeA:
		return _IP4Query
	case dns.TypeAAAA:
		return _IP6Query
	default:
		return notIPQuery
	}
}

// UnFqdn function
func UnFqdn(s string) string {
	if dns.IsFqdn(s) {
		return s[:len(s)-1]
	}
	return s
}
