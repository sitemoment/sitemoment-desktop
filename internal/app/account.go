// SPDX-License-Identifier: AGPL-3.0-or-later

// Package app holds the services bound to the frontend. They are plain
// structs; main.go registers them with Wails.
package app

import (
	"context"
	"errors"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/oauthx"
)

// Opener opens a URL in the user's browser.
type Opener func(url string) error

// loginTimeout is how long sign-in waits for the browser.
const loginTimeout = 5 * time.Minute

// Account is the SiteMoment account shown in the app, with this month's
// credit pool (0/0 when the server does not report one).
type Account struct {
	SignedIn     bool    `json:"signedIn"`
	Email        string  `json:"email"`
	Name         string  `json:"name"`
	AvatarURL    string  `json:"avatarUrl"`
	Plan         string  `json:"plan"`
	CreditsMonth string  `json:"creditsMonth"`
	CreditsUsed  float64 `json:"creditsUsed"`
	CreditsLimit float64 `json:"creditsLimit"`
}

// AccountService signs in to SiteMoment.
type AccountService struct {
	Cloud *cloud.Client
	Open  Opener
}

// Get returns the account, or an empty Account when signed out.
func (a *AccountService) Get(ctx context.Context) (Account, error) {
	me, err := a.Cloud.Me(ctx)
	if errors.Is(err, cloud.ErrSignedOut) {
		return Account{}, nil
	}
	if err != nil {
		return Account{}, err
	}
	acct := Account{SignedIn: true, Email: me.User.Email, Name: me.User.Name, AvatarURL: me.User.AvatarURL, Plan: me.Plan.Name}
	if me.Credits != nil {
		acct.CreditsMonth, acct.CreditsUsed, acct.CreditsLimit = me.Credits.Month, me.Credits.Used, me.Credits.Limit
	}
	return acct, nil
}

// Login opens the browser and waits for the sign-in to come back.
func (a *AccountService) Login(ctx context.Context) (Account, error) {
	r, err := oauthx.Listen()
	if err != nil {
		return Account{}, err
	}
	defer r.Close()
	verifier, challenge := oauthx.NewPKCE()
	state := oauthx.RandomState()
	if err := a.Open(a.Cloud.LoginURL(r.RedirectURI(), state, challenge)); err != nil {
		return Account{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	code, err := r.Wait(ctx, state)
	if err != nil {
		return Account{}, err
	}
	if err := a.Cloud.Exchange(ctx, code, verifier, r.RedirectURI()); err != nil {
		return Account{}, err
	}
	return a.Get(ctx)
}

// Logout signs out of SiteMoment.
func (a *AccountService) Logout(ctx context.Context) error { return a.Cloud.Logout(ctx) }
