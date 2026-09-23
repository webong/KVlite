# typed: false
# frozen_string_literal: true

# KVLite LevelDB driver bundle (pure Go, no system dependencies). Ships only
# its versioned catalog subtree; any installed kvlite host drives it, so this
# package owns no bin/kvlite link. See packaging/README.md.
class KvliteLeveldb < Formula
  desc "KVLite LevelDB storage driver bundle"
  homepage "https://github.com/webong/KVlite"
  # Pin tag and revision to the published release when cutting one; the tag
  # below is a placeholder until the first packaged release exists.
  url "https://github.com/webong/KVlite.git",
      tag: "v0.1.0"
  license "Apache-2.0"
  version "0.1.0"

  depends_on "go" => :build
  depends_on "kvlite"

  def install
    ENV["CGO_ENABLED"] = "1"
    system "make", "release", "RELEASE_VERSION=#{version}", "DRIVER=leveldb"
    system "bash", "scripts/install.sh",
           "--prefix", "#{prefix}",
           "--version", "#{version}",
           "--no-cli-link",
           "--component", "drivers"
  end

  def caveats
    <<~EOS
      Serve it through any installed kvlite host:

        export KVLITE_SYSTEM_MODULE_PATH="#{opt_lib}/kvlite"
        kvlite serve --path ./data --driver leveldb --listen 127.0.0.1:8080
    EOS
  end

  test do
    ENV["KVLITE_SYSTEM_MODULE_PATH"] = "#{opt_lib}/kvlite"
    ENV["KVLITE_MODULE_PATH"] = ""
    ENV["KVLITE_HOME"] = ""
    host = Formula["kvlite"].opt_bin/"kvlite"
    system host.to_s, "module", "verify", "leveldb"
  end
end
