# clipal

Install the Clipal CLI from npm:

```bash
npm install -g clipal
clipal --version
```

This package installs the platform-specific Clipal binary through npm optional dependencies. It does **not** use install scripts, so npm 12 and newer install it without `--allow-scripts`.

If your npm configuration skips optional dependencies, reinstall with them enabled:

```bash
npm install -g clipal --include=optional
```

The wrapper also keeps a fallback: if the platform package is missing, it downloads the matching binary from the official GitHub Releases and verifies `checksums.txt`.

> Older Clipal npm versions used a `postinstall` download. If you are installing one of those versions with npm 12+, use `npm install -g clipal --allow-scripts=clipal`.

Project documentation:

- Repository: https://github.com/PAIArtCom/Clipal
- Releases: https://github.com/PAIArtCom/Clipal/releases
