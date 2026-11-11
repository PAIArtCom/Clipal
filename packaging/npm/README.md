# clipal

Install the Clipal CLI from npm:

```bash
npm install -g clipal
clipal --version
```

## npm 12 and newer

npm 12 blocks dependency install scripts by default. Allow Clipal's downloader before installing (recommended, so future `npm update -g clipal` keeps working):

```bash
npm config set allow-scripts=clipal --location=user
npm install -g clipal
```

For a one-off install:

```bash
npm install -g clipal --allow-scripts=clipal
```

If an earlier install was blocked, rerun with the flag or rebuild:

```bash
npm install -g clipal --allow-scripts=clipal
# or
npm rebuild -g clipal --allow-scripts=clipal
```

> npm's own suggestion, `npm install -g --allow-scripts=clipal`, is missing the package name and fails outside a project. Use the commands above instead.

This npm package installs the platform-specific Clipal binary from the official GitHub Releases for the matching version.

Project documentation:

- Repository: https://github.com/PAIArtCom/Clipal
- Releases: https://github.com/PAIArtCom/Clipal/releases
