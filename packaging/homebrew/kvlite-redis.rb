# typed: false
# frozen_string_literal: true

# KVLite standalone Redis-compatible protocol module. Serves an owned
# directory directly, or attaches to a kvlite-http owner over loopback.
# Needs the kvlite host formula plus one driver (or the built-in memory
# engine for scratch). See packaging/README.md.
class KvliteRedis < Formula
  desc "KVLite standalone Redis-compatible protocol module"
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
    system "make", "release-redis", "RELEASE_VERSION=#{version}"
    system "bash", "scripts/install.sh",
           "--prefix", "#{prefix}",
           "--version", "#{version}",
           "--component", "modules"
  end

  test do
    ENV["KVLITE_SYSTEM_MODULE_PATH"] = "#{opt_lib}/kvlite"
    ENV["KVLITE_MODULE_PATH"] = ""
    ENV["KVLITE_HOME"] = ""
    host = Formula["kvlite"].opt_bin/"kvlite"
    system host.to_s, "module", "verify", "redis"
    port = free_port
    pid = spawn bin/"kvlite-redis", "--path", (testpath/"data").to_s,
                "--driver", "memory", "--listen", "127.0.0.1:#{port}",
                err: "/dev/null"
    begin
      require "socket"
      socket = nil
      50.times do
        begin
          socket = TCPSocket.new("127.0.0.1", port)
          break
        rescue SystemCallError, IOError
          sleep 0.2
        end
      end
      raise "Redis server did not start" if socket.nil?
      socket.write("*1\r\n$4\r\nPING\r\n")
      raise "PING failed" unless socket.gets&.strip == "+PONG"
    ensure
      socket&.close
      Process.kill("TERM", pid)
      Process.wait(pid)
    end
  end
end
