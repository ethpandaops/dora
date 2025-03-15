package services

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type CallRateLimiter struct {
	proxyCount uint
	rateLimit  uint
	burstLimit uint

	mutex    sync.Mutex
	visitors map[string]*callRateVisitor
}

type callRateVisitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

var GlobalCallRateLimiter *CallRateLimiter

// StartCallRateLimiter is used to start the global frontend cache service
func StartCallRateLimiter(proxyCount uint, rateLimit uint, burstLimit uint) error {
	if GlobalCallRateLimiter != nil {
		return nil
	}

	GlobalCallRateLimiter = &CallRateLimiter{
		proxyCount: proxyCount,
		rateLimit:  rateLimit,
		burstLimit: burstLimit,

		visitors: map[string]*callRateVisitor{},
	}
	go GlobalCallRateLimiter.cleanupVisitors()

	return nil
}

func (crl *CallRateLimiter) CheckCallLimit(r *http.Request, callCost uint) error {
	if crl == nil {
		return nil
	}
	visitor := crl.getVisitor(r)
	if visitor == nil {
		return fmt.Errorf("could not get visitor")
	}
	if !visitor.limiter.AllowN(time.Now(), int(callCost)) {
		return fmt.Errorf("call rate limit exceeded")
	}
	return nil
}

func (crl *CallRateLimiter) getVisitor(r *http.Request) *callRateVisitor {
	var ip string

	if crl.proxyCount > 0 {
		forwardIps := strings.Split(r.Header.Get("X-Forwarded-For"), ", ")
		forwardIdx := len(forwardIps) - int(crl.proxyCount)
		if forwardIdx >= 0 {
			ip = forwardIps[forwardIdx]
		}
	}
	if ip == "" {
		var err error
		ip, _, err = net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return nil
		}
	}

	crl.mutex.Lock()
	defer crl.mutex.Unlock()

	visitor := crl.visitors[ip]
	if visitor == nil {
		visitor = &callRateVisitor{
			limiter:  rate.NewLimiter(rate.Limit(crl.rateLimit), int(crl.burstLimit)),
			lastSeen: time.Now(),
		}
		crl.visitors[ip] = visitor
	} else {
		visitor.lastSeen = time.Now()
	}
	return visitor
}

func (crl *CallRateLimiter) cleanupVisitors() {
	for {
		time.Sleep(time.Minute)

		crl.mutex.Lock()
		for ip, v := range crl.visitors {
			if time.Since(v.lastSeen) > 3*time.Minute {
				delete(crl.visitors, ip)
			}
		}
		crl.mutex.Unlock()
	}
}
