# typed: false
# frozen_string_literal: true

# KVLite driverless host CLI: core plus the ephemeral memory engine, no
# persistent storage linked or bundled. It discovers installed driver and
# protocol bundles through KVLITE_SYSTEM_MODULE_PATH and verifies them
# before running or loading anything. Install a driver formula (kvlite-leveldb
# or kvlite-rocksdb) for persistence and the protocol formulae for servers.
# See packaging/README.md.
class Kvlite < Formula
  desc "Pluggable key-value host CLI with ephemeral memory engine"
  homepage "https://github.com/webong/KVlite"
  # Pin tag and revision to the published release when cutting one; the tag
  # below is a placeholder until the first packaged release exists.
  url "https://github.com/webong/KVlite.git",
      tag: "v0.1.0"
  license "Apache-2.0"
  version "0.1.0"

  depends_on "go" => :build

  def install
    ENV["CGO_ENABLED"] = "1"
    system "make", "release", "RELEASE_VERSION=#{version}", "DRIVER=none"
    system "bash", "scripts/install.sh",
           "--prefix", "#{prefix}",
           "--version", "#{version}"
  end

  def caveats
    <<~EOS
      This is the driverless host: it serves ephemeral memory databases and
      discovers installed drivers. For persistence, install kvlite-leveldb
      (or kvlite-rocksdb); for servers, kvlite-http and kvlite-redis.
      Point discovery at every installed catalog root, e.g.:

        export KVLITE_SYSTEM_MODULE_PATH="#{opt_lib}/kvlite"
    EOS
  end

  test do
    ENV["KVLITE_SYSTEM_MODULE_PATH"] = "#{opt_lib}/kvlite"
    ENV["KVLITE_MODULE_PATH"] = ""
    ENV["KVLITE_HOME"] = ""
    assert_match "memory", shell_output("#{bin}/kvlite driver list")
    system bin/"kvlite", "module", "list"
  end
end
