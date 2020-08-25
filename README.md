# sqlfmt
Experimental SQL formatter with width-aware output

Based on http://homepages.inf.ed.ac.uk/wadler/papers/prettier/prettier.pdf.

Requires having the master branch of https://github.com/cockroachdb/cockroach checked out at `$GOPATH/src/github.com/cockroachdb/cockroach`, and running `make` in that directory.

# Deployment

To deploy:

1. Checkout this repo to `$GOPATH/src/github.com/cockroachdb/sqlfmt`
2. Checkout cockroach to `$GOPATH/src/github.com/cockroachdb/cockroach`
3. `cd` to `$GOPATH/src/github.com/cockroachdb/cockroach`
4. Run `make`
5. `cd` to `$GOPATH/src/github.com/cockroachdb/sqlfmt`
6. Ensure your kubectl creds for the desired cluster are present.
7. Run `./build.sh`
8. If everything went well, kill current pods so they will be redeployed from the new docker image:
`kubectl get po | grep sqlfmt | awk '{print $1}' | xargs kubectl delete po`
