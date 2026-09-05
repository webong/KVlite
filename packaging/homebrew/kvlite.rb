# typed: false
# frozen_string_literal: true

# KVLite with the pure-Go LevelDB driver plus standalone HTTP and Redis
# protocol modules. The installed CLI is extension-free: it discovers the
# installed bundles through KVLITE_SYSTEM_MODULE_PATH and verifies them
# before running or loading anything. See packaging/README.md.
class Kvlite < Formula
  desc "Engine-neutral typed key-value store with installable driver and protocol modules"
  homepage "https://github.com/webong/KVlite"
  # Pin tag and revision to the published release when cutting one; the tag
  # below is a placeholder until the first packaged release exists.
  url "https://github.com/webong/KVlite.git",
      tag:      "v0.1.0"
  license "Apache-2.0"
  version "0.1.0"

  depends_on "go" => :build

  def install
    ENV["CGO_ENABLED"] = "1"
    system "make", "release", "RELEASE_VERSION=#{version}", "DRIVER=leveldb"
    system "make", "release-http", "RELEASE_VERSION=#{version}"
    system "make", "release-redis", "RELEASE_VERSION=#{version}"
    system "bash", "scripts/install.sh",
           "--prefix=#{prefix}",
           "--version=#{version}",
           "--link-cli=leveldb"
  end

  def caveats
    <<~EOS
      KVLite discovers installed modules through the system catalog root.
      Add to your shell profile (or service definition):

        export KVLITE_SYSTEM_MODULE_PATH="#{opt_lib}/kvlite"
    EOS
  end

  test do
    ENV["KVLITE_SYSTEM_MODULE_PATH"] = "#{opt_lib}/kvlite"
    ENV["KVLITE_MODULE_PATH"] = ""
    ENV["KVLITE_HOME"] = ""
    system bin/"kvlite", "module", "verify", "leveldb"
    system bin/"kvlite", "module", "verify", "http"
    system bin/"kvlite", "module", "verify", "redis"
  end
end
