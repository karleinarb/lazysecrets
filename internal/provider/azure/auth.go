package azure

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// authCredential turns every token failure into a *provider.AuthError, so
// the UI can tell "log in again" apart from other failures however deep in
// the SDK the token was requested.
type authCredential struct {
	cred    azcore.TokenCredential
	login   []string
	account string // label when there are several accounts
}

func (c authCredential) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	tok, err := c.cred.GetToken(ctx, opts)
	if err != nil && ctx.Err() == nil {
		err = &provider.AuthError{Kind: provider.KindAzure, Account: c.account, Login: c.login, Err: err}
	}
	return tok, err
}
