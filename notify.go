// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// nativeNotes sends OS notifications through Wails' notifications service
// (app.NativeNotifier). The service refuses to start without a bundle
// identifier (macOS outside a .app, e.g. `go run .`), and Wails stops the
// whole app when a service fails to start; this wrapper swallows that, and
// the app keeps its in-app notifications.
type nativeNotes struct {
	svc *notifications.NotificationService

	mu  sync.Mutex
	ok  bool
	why string
}

func newNativeNotes() *nativeNotes {
	return &nativeNotes{svc: notifications.New(), why: "系统通知还没有启动"}
}

func (n *nativeNotes) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	err := n.svc.ServiceStartup(ctx, options)
	n.mu.Lock()
	defer n.mu.Unlock()
	if err != nil {
		n.why = "系统通知不可用：" + err.Error()
		log.Printf("notifications: OS notifications off: %v", err)
		return nil
	}
	n.ok, n.why = true, ""
	return nil
}

func (n *nativeNotes) ServiceShutdown() error {
	if ok, _ := n.Available(); ok {
		return n.svc.ServiceShutdown()
	}
	return nil
}

func (n *nativeNotes) Available() (bool, string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ok, n.why
}

func (n *nativeNotes) Send(title, body string) error {
	if ok, why := n.Available(); !ok {
		return errors.New(why)
	}
	allowed, err := n.svc.CheckNotificationAuthorization()
	if err == nil && !allowed {
		allowed, err = n.svc.RequestNotificationAuthorization()
	}
	if err != nil {
		return fmt.Errorf("系统通知：%w", err)
	}
	if !allowed {
		return errors.New("系统通知未获允许：请在系统设置的「通知」里允许 SiteMoment")
	}
	return n.svc.SendNotification(notifications.NotificationOptions{ID: fmt.Sprintf("sitemoment-%d", time.Now().UnixNano()), Title: title, Body: body})
}
