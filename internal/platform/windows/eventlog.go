// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wevtapi          = windows.NewLazySystemDLL("wevtapi.dll")
	procEvtSubscribe = wevtapi.NewProc("EvtSubscribe")
	procEvtNext      = wevtapi.NewProc("EvtNext")
	procEvtRender    = wevtapi.NewProc("EvtRender")
	procEvtClose     = wevtapi.NewProc("EvtClose")
)

const (
	evtSubscribeToFutureEvents = 1
	evtRenderEventXML          = 1
	evtBatch                   = 16
	maxEventXML                = 256 << 10
)

// FollowEventLog subscribes to new events in channel (for example
// Application or System) and passes each one on as one line of text.
func (Platform) FollowEventLog(ctx context.Context, channel string, onLine func(string)) error {
	if err := wevtapi.Load(); err != nil {
		return fmt.Errorf("the Windows event log API is not available: %w", err)
	}
	signal, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(signal)
	ch, err := windows.UTF16PtrFromString(channel)
	if err != nil {
		return err
	}
	query, _ := windows.UTF16PtrFromString("*")
	sub, _, callErr := procEvtSubscribe.Call(0, uintptr(signal), uintptr(unsafe.Pointer(ch)),
		uintptr(unsafe.Pointer(query)), 0, 0, 0, evtSubscribeToFutureEvents)
	if sub == 0 {
		return fmt.Errorf("subscribe to the %s event log: %w", channel, callErr)
	}
	defer procEvtClose.Call(sub)

	for ctx.Err() == nil {
		ev, err := windows.WaitForSingleObject(signal, 1000)
		if err != nil {
			return err
		}
		if ev != windows.WAIT_OBJECT_0 {
			continue
		}
		if err := drain(sub, onLine); err != nil {
			return err
		}
	}
	return nil
}

func drain(sub uintptr, onLine func(string)) error {
	var handles [evtBatch]uintptr
	for {
		var got uint32
		r, _, err := procEvtNext.Call(sub, evtBatch, uintptr(unsafe.Pointer(&handles[0])), 0, 0, uintptr(unsafe.Pointer(&got)))
		if r == 0 {
			if errors.Is(err, syscall.Errno(windows.ERROR_NO_MORE_ITEMS)) {
				return nil
			}
			return fmt.Errorf("read events: %w", err)
		}
		for _, h := range handles[:got] {
			if xml, err := render(h); err == nil {
				onLine(EventText(xml))
			}
			procEvtClose.Call(h)
		}
	}
}

func render(h uintptr) (string, error) {
	var used, props uint32
	procEvtRender.Call(0, h, evtRenderEventXML, 0, 0, uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&props)))
	if used == 0 || used > maxEventXML {
		return "", errors.New("event too large or empty")
	}
	buf := make([]uint16, used/2+1)
	r, _, err := procEvtRender.Call(0, h, evtRenderEventXML, uintptr(used), uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&props)))
	if r == 0 {
		return "", err
	}
	return windows.UTF16ToString(buf), nil
}

var (
	providerRe = regexp.MustCompile(`<Provider\s+Name=['"]([^'"]*)['"]`)
	eventIDRe  = regexp.MustCompile(`<EventID[^>]*>(\d+)</EventID>`)
	levelRe    = regexp.MustCompile(`<Level>(\d)</Level>`)
	systemRe   = regexp.MustCompile(`(?s)<System>.*?</System>`)
	tagRe      = regexp.MustCompile(`<[^>]+>`)
)

var levels = map[string]string{"1": "critical", "2": "error", "3": "warning", "4": "information", "0": "information", "5": "verbose"}

// EventText turns an event's XML into one readable line:
// "provider=X id=N level=error <the event's data>". Patterns match against it.
func EventText(xml string) string {
	var parts []string
	if m := providerRe.FindStringSubmatch(xml); m != nil {
		parts = append(parts, "provider="+m[1])
	}
	if m := eventIDRe.FindStringSubmatch(xml); m != nil {
		parts = append(parts, "id="+m[1])
	}
	if m := levelRe.FindStringSubmatch(xml); m != nil {
		parts = append(parts, "level="+levels[m[1]])
	}
	data := tagRe.ReplaceAllString(systemRe.ReplaceAllString(xml, " "), " ")
	data = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&amp;", "&").Replace(data)
	if d := strings.Join(strings.Fields(data), " "); d != "" {
		parts = append(parts, d)
	}
	return strings.Join(parts, " ")
}
