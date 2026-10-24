import type { Ref } from 'react';
import { Button } from '@cofy-x/axv-nextstep';

const profileExample = JSON.stringify(
  {
    cloudflare: {
      enabled: false,
      listen: '127.0.0.1:3002',
      origin: 'https://axv.example.com',
      issuer: 'https://your-team.cloudflareaccess.com',
      audience: 'your-access-application-aud',
      owner_subject: 'your-stable-access-user-id',
    },
  },
  null,
  2,
);

const tunnelExample = `ingress:
  - hostname: axv.example.com
    service: http://127.0.0.1:3002
    originRequest:
      httpHostHeader: axv.example.com
  - service: http_status:404`;

export function SetupSteps() {
  return (
    <ol className="cloudflare-setup-steps" aria-label="Cloudflare setup steps">
      <li>Create a Cloudflare Access application and Tunnel.</li>
      <li>
        Add the settings to this AXV profile’s <code>config.json</code>.
      </li>
      <li>Choose Refresh, then Enable.</li>
    </ol>
  );
}

export function SetupGuide({
  headingRef,
  interactive = true,
  onClose,
}: {
  headingRef?: Ref<HTMLHeadingElement>;
  interactive?: boolean;
  onClose: () => void;
}) {
  return (
    <section
      id="cloudflare-setup-guide"
      className="cloudflare-setup-guide"
      aria-labelledby="cloudflare-setup-title"
    >
      <h3 id="cloudflare-setup-title" ref={headingRef} tabIndex={-1}>
        Cloudflare setup guide
      </h3>
      <p>
        Use the Setup form to save desktop access settings. This advanced guide
        shows the equivalent manual configuration. Create the Access application
        and tunnel, and run the connector separately. The optional account
        wizard lets AXV manage DNS and ingress for app publications.
      </p>
      <ol className="cloudflare-guide-steps">
        <li>
          <h4>Protect your desktop hostname</h4>
          <p>
            Create a self-hosted Access application for the entire hostname,
            with an explicit owner policy and MFA, before routing public
            traffic. Record the team domain and application audience (AUD). Use
            your stable Access user ID, the application token’s <code>sub</code>
            , as the owner subject, rather than an email address. Set the
            application session duration to 15 minutes or less.
          </p>
          <p className="cloudflare-guide-links">
            <a
              href="https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/self-hosted-public-app/"
              target="_blank"
              rel="noopener noreferrer"
            >
              Create an Access application
            </a>
            <a
              href="https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/application-token/"
              target="_blank"
              rel="noopener noreferrer"
            >
              Find your Access identity
            </a>
          </p>
        </li>
        <li>
          <h4>Configure the current AXV profile</h4>
          <p>
            Merge this <code>cloudflare</code> object into{' '}
            <code>AXV_HOME/config.json</code> for the profile running this
            desktop. Preserve its other settings and replace the example values.
            The normal profile defaults to <code>~/.axv</code>; development and
            custom profiles can use another location.
          </p>
          <pre aria-label="Example AXV profile configuration">
            <code>{profileExample}</code>
          </pre>
          <p>
            Keep the public HTTPS hostname in <code>origin</code> and the team
            HTTPS address in <code>issuer</code>, without trailing slashes. Keep
            Tunnel connector credentials and Access JWTs outside this profile.
            The account wizard stores its scoped management API token in the AXV
            profile. Disable desktop access before changing its settings.
          </p>
        </li>
        <li>
          <h4>Connect the tunnel and enable access</h4>
          <p>
            Run your tunnel connector on the AXV computer and point its public
            hostname to <code>http://127.0.0.1:3002</code>, or your configured
            loopback address. A locally managed tunnel can use:
          </p>
          <pre aria-label="Example Cloudflare Tunnel ingress">
            <code>{tunnelExample}</code>
          </pre>
          <p>
            Preserve the public Host and Origin. Do not point the tunnel at
            AXV’s ordinary local desktop port. Keep your existing tunnel
            identity and credential configuration outside AXV.
          </p>
          <p className="cloudflare-guide-links">
            <a
              href="https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/configuration-file/"
              target="_blank"
              rel="noopener noreferrer"
            >
              Configure your tunnel
            </a>
            <a
              href="https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/origin-parameters/"
              target="_blank"
              rel="noopener noreferrer"
            >
              Tunnel origin settings
            </a>
          </p>
          <p>
            Open Overview, choose Refresh, then Enable. When AXV says Listening
            locally, open the Desktop URL in another browser to verify that your
            owner can sign in and other users cannot. AXV does not check public
            tunnel connectivity.
          </p>
        </li>
      </ol>
      <Button disabled={!interactive} onClick={onClose}>
        Close guide
      </Button>
    </section>
  );
}
