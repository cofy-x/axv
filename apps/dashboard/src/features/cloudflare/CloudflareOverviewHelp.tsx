export function CloudflareOverviewHelp() {
  return (
    <details className="cloudflare-disclosure">
      <summary>Help</summary>
      <div className="cloudflare-disclosure-body">
        <p>
          AXV manages its desktop entrypoint and can publish VM services using
          your connected account. Create the Cloudflare Tunnel and Access
          applications separately, and run your tunnel connector on the AXV
          host.
        </p>
        <p>
          Closing this window keeps access enabled. Enable and disable choices
          are saved for future launches.
        </p>
        <p>
          Tunnel health checks verify the token, DNS read access and connector
          status. They do not verify public URL reachability, DNS write
          permissions or WAF protection.
        </p>
        <p>
          When your Access session expires, its connections close: host shells
          end and Codex views detach while Codex runs and VMs continue. Use an
          Access application session of 15 minutes or less. Provider logout or
          policy changes are not detected immediately; the original token expiry
          still limits an open connection. Sign in again to reconnect.
        </p>
        <p className="cloudflare-guide-links">
          <a
            href="https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/self-hosted-public-app/"
            target="_blank"
            rel="noopener noreferrer"
          >
            Cloudflare Access guide
          </a>
          <a
            href="https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/configuration-file/"
            target="_blank"
            rel="noopener noreferrer"
          >
            Cloudflare Tunnel guide
          </a>
        </p>
      </div>
    </details>
  );
}
