<?php

require __DIR__ . '/vendor/autoload.php';

use Spiral\RoadRunner\Worker;
use Spiral\RoadRunner\Tcp\TcpWorker;
use Spiral\RoadRunner\Tcp\TcpResponse;
use Spiral\RoadRunner\Tcp\TcpEvent;

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
            'remote_addr' => $request->getRemoteAddress(),
            'server' => $request->getServer(),
            'uuid' => $request->getConnectionUuid(),
            'body' => $request->getBody(),
            'event' => $request->getEvent(),
        ]));
    } catch (\Throwable $e) {
        $tcpWorker->respond("Something went wrong: " . $e->getMessage() . "\r\n", TcpResponse::RespondClose);
        $worker->error((string)$e);
        continue;
    }
}
