# Install release-cli from native package repositories

Use this guide to install and update `release-cli` from the signed APT, DNF, or APK repository at `https://pkgs.meigma.dev`.

The repository publishes amd64 and arm64 packages. Each client verifies both repository metadata and the producer package before installation. Do not disable either check when recovering a failed install.

## Prerequisites

You need:

- root access on the target host;
- a working system clock and HTTPS certificate store;
- Debian or Ubuntu with APT, a DNF-based Linux distribution, or Alpine Linux.

## Install with APT

Install the HTTPS prerequisites from the distribution repositories:

```sh
sudo apt-get update
sudo apt-get install -y ca-certificates curl
```

Install the repository key in a dedicated keyring file:

```sh
sudo install -d -m 0755 /etc/apt/keyrings
curl --fail --silent --show-error --location \
  https://pkgs.meigma.dev/keys/apt-repository-001.asc \
  | sudo tee /etc/apt/keyrings/meigma-packages.asc >/dev/null
sudo chmod 0644 /etc/apt/keyrings/meigma-packages.asc
```

Add the signed repository and install the command:

```sh
printf '%s\n' \
  'deb [signed-by=/etc/apt/keyrings/meigma-packages.asc] https://pkgs.meigma.dev/apt stable main' \
  | sudo tee /etc/apt/sources.list.d/meigma-packages.list >/dev/null
sudo apt-get update
sudo apt-get install -y release-cli
```

Confirm the installed package:

```sh
dpkg-query --show --showformat='${Package} ${Version} ${Architecture}\n' release-cli
```

## Install with DNF

Create a repository definition that checks the aggregate repository signature and the producer RPM signature:

```sh
sudo tee /etc/yum.repos.d/meigma-packages.repo >/dev/null <<'EOF'
[meigma-packages]
name=Meigma packages
baseurl=https://pkgs.meigma.dev/rpm/stable/$basearch
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://pkgs.meigma.dev/keys/rpm-repository-001.asc https://pkgs.meigma.dev/keys/release-rpm-001.asc
EOF
```

Install and inspect the package:

```sh
sudo dnf install -y release-cli
rpm -q --qf '%{NAME} %{VERSION}-%{RELEASE} %{ARCH}\n' release-cli
```

Keep both `gpgcheck=1` and `repo_gpgcheck=1`. The first checks the producer-signed RPM; the second checks aggregate repository metadata.

## Install with APK

APK identifies signing keys by filename. Keep the published filenames unchanged when you install them:

```sh
sudo wget -q \
  https://pkgs.meigma.dev/keys/apk-index-001.rsa.pub \
  -O /etc/apk/keys/apk-index-001.rsa.pub
sudo wget -q \
  https://pkgs.meigma.dev/keys/meigma-release-001.rsa.pub \
  -O /etc/apk/keys/meigma-release-001.rsa.pub
```

Add the repository, refresh its signed index, and install the command:

```sh
printf '%s\n' 'https://pkgs.meigma.dev/apk/stable/main' \
  | sudo tee -a /etc/apk/repositories >/dev/null
sudo apk update
sudo apk add release-cli
apk info --installed release-cli
```

The aggregate key verifies `APKINDEX.tar.gz`. The producer key verifies the package. Both keys are required.

## Update release-cli

Use the package manager's normal update path:

```sh
sudo apt-get update && sudo apt-get install --only-upgrade release-cli
```

```sh
sudo dnf upgrade release-cli
```

```sh
sudo apk update && sudo apk upgrade release-cli
```

The repository retains published versions. A normal install selects the newest version accepted by the client unless the host has a package pin or version lock.

## Recover a failed install

### TLS verification fails

Check the system clock and update the host's CA certificate package from its distribution repository. Do not replace `https://pkgs.meigma.dev` with HTTP and do not disable certificate verification.

### A repository or package signature is rejected

Stop before installing the package. Do not use APT `trusted=yes`, DNF `gpgcheck=0`, APK `--allow-untrusted`, or a similar bypass.

Confirm that the configured key URLs and filenames match the reviewed package policy. The current public names are:

- `apt-repository-001.asc` for APT metadata;
- `rpm-repository-001.asc` and `release-rpm-001.asc` for DNF metadata and packages;
- `apk-index-001.rsa.pub` and `meigma-release-001.rsa.pub` for APK indexes and packages.

A key replacement uses a new reviewed public-key object; immutable key objects are not overwritten. Replace keys manually:

1. Obtain the new key URL, expected fingerprint or digest, and activation notice through the trusted release channel.
2. Download the new key to a temporary file over HTTPS.
3. Compare its fingerprint or digest with the independently reviewed value.
4. Install the new key alongside the old key. For APK, preserve the announced basename exactly.
5. Update the APT `signed-by` path, DNF `gpgkey` list, or APK key set to include the new key.
6. Refresh repository metadata and complete one verified installation.
7. Remove the retired key only after the refreshed metadata and package both verify.

If no reviewed replacement exists, leave the old configuration in place and report the failure. A repeated signature error can indicate stale metadata, an incomplete key rollout, or tampering; bypassing verification hides the distinction.

### The requested version is unavailable

Refresh metadata, then inspect the versions visible to the client:

```sh
apt-cache policy release-cli
```

```sh
dnf --showduplicates list release-cli
```

```sh
apk policy release-cli
```

If the release is visible on GitHub but absent from the package repository, wait for the central publication workflow or contact its operator. Do not install an unverified package copied from a failed publication run.

### An update was interrupted

Run the metadata refresh and install command again. Publication is convergent: immutable package objects are never overwritten, and repository roots become visible only after their referenced objects are uploaded. A replay either completes the same state or fails without accepting conflicting immutable content.

For repository operation and replay procedures, see [Set up the shared package repository](set-up-package-repository.md). The [package repository contract](../reference/package-repository-contract.md) defines the public paths, trust checks, cache behavior, and publication states.
