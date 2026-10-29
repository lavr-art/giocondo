#!/bin/sh
# Adds deb packages to the apt repository (a directory for GitHub Pages) and signs it.
#
#   deploy/apt/update-repo.sh REPO_DIR GPG_KEY_ID file.deb...
#
# Layout: pool/main/*.deb, dists/stable/main/binary-{amd64,arm64}/Packages[.gz],
# dists/stable/{Release,InRelease,Release.gpg}, vpnctl.gpg is the public key (binary, for signed-by).
# Old versions are not removed from pool. Requires apt-ftparchive (apt-utils), gpg and the key in the keyring.
set -eu

[ $# -ge 3 ] || { sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 1; }
REPO=$1 KEY=$2
shift 2

mkdir -p "$REPO/pool/main"
cp "$@" "$REPO/pool/main/"
cd "$REPO"

for arch in amd64 arm64; do
  dir=dists/stable/main/binary-$arch
  mkdir -p "$dir"
  apt-ftparchive --arch "$arch" packages pool/main > "$dir/Packages"
  gzip -9nkf "$dir/Packages"
done

apt-ftparchive \
  -o APT::FTPArchive::Release::Origin=vpnctl \
  -o APT::FTPArchive::Release::Label=vpnctl \
  -o APT::FTPArchive::Release::Suite=stable \
  -o APT::FTPArchive::Release::Codename=stable \
  -o APT::FTPArchive::Release::Architectures="amd64 arm64" \
  -o APT::FTPArchive::Release::Components=main \
  release dists/stable > dists/stable/Release.new
mv dists/stable/Release.new dists/stable/Release

rm -f dists/stable/InRelease dists/stable/Release.gpg
gpg --batch --yes --local-user "$KEY" --clearsign -o dists/stable/InRelease dists/stable/Release
gpg --batch --yes --local-user "$KEY" --detach-sign -o dists/stable/Release.gpg dists/stable/Release
gpg --batch --yes --export "$KEY" > vpnctl.gpg
touch .nojekyll # Pages serves files as is
echo "repo updated: $(ls pool/main | wc -l) packages"
