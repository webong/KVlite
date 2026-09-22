# typed: false
# frozen_string_literal: true

# KVLite with the RocksDB storage driver, composed with the system engine:
# this formula links Homebrew's rocksdb (plus its compression libraries)
# instead of bundling them. KVLite supports librocksdb.so.10
# (v10.8.3-v10.10.1 tested); only LZ4 support is required at runtime.
# Prefer a --bundle-runtime package wherever the system library cannot be
# pinned. See packaging/README.md ("Bring your own engine").
class KvliteRocksdb < Formula
  desc "KVLite key-value store with the RocksDB storage driver (system engine)"
  homepage "https://github.com/webong/KVlite"
  # Pin tag and revision to the published release when cutting one; the tag
  # below is a placeholder until the first packaged release exists.
  url "https://github.com/webong/KVlite.git",
      tag: "v0.1.0"
  license "Apache-2.0"
  version "0.1.0"

  depends_on "go" => :build
  depends_on "rocksdb"
  depends_on "lz4"
  depends_on "snappy"
  depends_on "zstd"

  def install
    ENV["CGO_ENABLED"] = "1"
    ENV.append "CFLAGS", "-I#{Formula["rocksdb"].opt_include}"
    ENV.append "CXXFLAGS", "-I#{Formula["rocksdb"].opt_include}"
    system "make", "release", "RELEASE_VERSION=#{version}", "DRIVER=rocksdb"
    # No CLI link: any installed kvlite host already drives every driver
    # bundle, so this complement package ships only its catalog subtree.
    system "bash", "scripts/install.sh",
           "--prefix", "#{prefix}",
           "--version", "#{version}",
           "--no-cli-link",
           "--component", "drivers"
  end

  def caveats
    <<~EOS
      This package provides the RocksDB driver only. Serve it through any
      installed kvlite host, e.g. from the kvlite formula:

        export KVLITE_SYSTEM_MODULE_PATH="#{opt_lib}/kvlite"
        kvlite serve --path ./data --driver rocksdb --listen 127.0.0.1:8080

      Install kvlite-http and kvlite-redis for the protocol modules.
    EOS
  end

  test do
    # The host CLI comes from the kvlite formula; here we only prove the
    # bundle tree is present and well-formed.
    assert_predicate opt_lib/"kvlite/drivers/rocksdb/kvlite-module.json", :exist?
  end
end
