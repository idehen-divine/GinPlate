// Package exceptions is the home for custom error handlers: domain error
// types plus their web.RegisterErrorMapper registrations (first match
// wins; match with errors.As so wrapped errors resolve). Scaffold with
// `ginplate make:exception`. Never import internal/modules/* (would cycle).
package exceptions
