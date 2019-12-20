FROM golang:1.13
ADD sqlfmt /
CMD ["/sqlfmt"]
