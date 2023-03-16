tag:

git tag v0.X.0
git push origin v0.x.0

build:

cd backend
export GITHUB_TOKEN=BLAH
goreleaser release
