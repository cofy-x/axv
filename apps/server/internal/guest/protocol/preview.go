package protocol

// Preview headers are private routing metadata, removed before reaching the
// project application. Path is an escaped absolute URL path; query is retained.
const PreviewBindingHeader = "X-AXV-Preview-Binding"
const PreviewScopeHeader = "X-AXV-Preview-Scope"
const PreviewPortHeader = "X-AXV-Preview-Port"
const PreviewPathHeader = "X-AXV-Preview-Path"
const PreviewAuthorizationHeader = "X-AXV-Preview-Application-Authorization"
