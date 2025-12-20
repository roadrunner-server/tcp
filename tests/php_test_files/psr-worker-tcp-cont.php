<?php

require __DIR__ . '/vendor/autoload.php';

use Spiral\RoadRunner\Worker;
use Spiral\RoadRunner\Tcp\TcpWorker;
use Spiral\RoadRunner\Tcp\TcpResponse;
use Spiral\RoadRunner\Tcp\TcpEvent;

// Create new RoadRunner worker from global environment
$worker = Worker::create();

$tcpWorker = new TcpWorker($worker);

while ($request = $tcpWorker->waitRequest()) {
    if (is_null($request)) {
        return;
    }
    try {
        if ($request->getEvent() === TcpEvent::Connected) {
            // Or send response to the TCP connection, for example, to the SMTP client
            $tcpWorker->respond("hello \r\n");
        } elseif ($request->getEvent() === TcpEvent::Data) {
            if ($request->getServer() === 'server1') {
                // Send response to the TCP connection and wait for the next request
                $tcpWorker->respond(json_encode([
                    'body' => $request->getBody(),
                    'uuid' => $request->getConnectionUuid(),
                    'remote_addr' => "foo1",
                ]));
            } elseif ($request->getServer() === 'server2') {
                // Send response to the TCP connection and wait for the next request
                $tcpWorker->respond(json_encode([
                    'body' => $request->getBody(),
                    'remote_addr' => "foo2",
                ]));
            } elseif (($request->getServer() === 'server3')) {
                // Send response to the TCP connection and wait for the next request
                $tcpWorker->respond(json_encode([
                    'body' => $request->getBody(),
                    'remote_addr' => "foo3",
                ]));
            }
            // Handle closed connection event
        } elseif ($request->getEvent() === TcpEvent::Close) {
            // Send response to the TCP connection and wait for the next request
            $tcpWorker->respond(json_encode([
                'body' => $request->getBody(),
                'remote_addr' => "foo3",
            ]));
        } else {
            $tcpWorker->respond(json_encode([
                'body' => $request->getBody(),
                'remote_addr' => "foo3",
            ]));
        }
    } catch (\Throwable $e) {
        $tcpWorker->respond("Something went wrong: " . $e->getMessage() . "\r\n", TcpResponse::RespondClose);
        $worker->error((string)$e);
    }
}
