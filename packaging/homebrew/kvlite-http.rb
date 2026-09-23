# typed: false
# frozen_string_literal: true

# KVLite standalone HTTP protocol module. Serves an owned directory
# directly, or owns one on behalf of an attached kvlite-redis. Needs the
# kvlite host formula plus one driver (kvlite-leveldb for persistence, or
# the built-in memory engine for scratch). See packaging/README.md.
class KvliteHttp < Formula
  desc "KVLite standalone JSON/HTTP protocol module"
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
    system "make", "release-http", "RELEASE_VERSION=#{version}"
    system "bash", "scripts/install.sh",
           "--prefix", "#{prefix}",
           "--version", "#{version}",
           "--component", "modules"
  end

  def caveats
    <<~EOS
      Serve an ephemeral memory database through any installed host:

        export KVLITE_SYSTEM_MODULE_PATH="#{opt_lib}/kvlite"
        kvlite serve --path ./data --driver memory --extension-mode standalone --listen 127.0.0.1:8080
    EOS
  end

  test do
    ENV["KVLITE_SYSTEM_MODULE_PATH"] = "#{opt_lib}/kvlite"
    ENV["KVLITE_MODULE_PATH"] = ""
    ENV["KVLITE_HOME"] = ""
    host = Formula["kvlite"].opt_bin/"kvlite"
    system host.to_s, "module", "verify", "http"
    port = free_port
    pid = spawn host.to_s, "serve", "--path", testpath/"data",
                "--driver", "memory", "--extension-mode", "standalone",
                "--listen", "127.0.0.1:#{port}", err: "/dev/null"
    begin
      require "net/http"
      require "json"
      body = nil
      50.times do
        begin
          body = Net::HTTP.get(URI("http://127.0.0.1:#{port}/v1/health"))
          break if body.include?("ok")
        rescue SystemCallError, IOError
          sleep 0.2
        end
      end
      raise "HTTP owner did not start" unless body&.include?("ok")
    ensure
      Process.kill("TERM", pid)
      Process.wait(pid)
    end
  end
end
