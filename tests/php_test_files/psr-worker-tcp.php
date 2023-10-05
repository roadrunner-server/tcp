<?php

require __DIR__ . '/vendor/autoload.php';

use Spiral\RoadRunner\Worker;
use Spiral\RoadRunner\Tcp\TcpWorker;

// Create new RoadRunner worker from global environment
$worker = Worker::create();

$tcpWorker = new TcpWorker($worker);

while (true) {
    try {
        $request = $tcpWorker->waitRequest();

        if (is_null($request)) {
            return;
        }

        $tcpWorker->respond(json_encode([
            'remote_addr' => $request->remoteAddr,
            'server' => $request->server,
            'uuid' => $request->connectionUuid,
            'body' => $request->body,
            'event' => $request->event
        ]));
    } catch (\Throwable $e) {
        $tcpWorker->respond("Something went wrong");

        $worker->error((string)$e);

        continue;
    }
}
