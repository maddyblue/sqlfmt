FROM golang:1.13-alpine
ADD sqlfmt /
CMD ["/sqlfmt"]
