# cargo-depsgraph

This is a tool for rendering dependency graphs from Cargo.lock files, with
a focus on helping figure out why there are multiple versions of a given
crate in the dependency graph.

## Example usage
To generate a dependency graph for, say, 10X Genomics's
[rust-toolbox](https://github.com/10XGenomics/rust-toolbox) workspace,
```bash
$ go get github.com/adam-azarchs/cargo-depsgraph
$ go install github.com/adam-azarchs/cargo-depsgraph
# assuming it was placed in your $PATH,
$ cargo-depsgraph -trim -dot \
    -baseurl https://github.com/10XGenomics/rust-toolbox/blob/master/ \
    Cargo.lock | dot -Tsvg -o tenx_rust_toolbox.svg
```

which renders as

![dependency graph](tenx_rust_toolbox.svg)

### Why did you write a tool for working with rust code in Go?

Because it's faster to develop in and, while I spend lots of time
working _around_ rust code, I don't actually spend much time _writing_
rust code.


