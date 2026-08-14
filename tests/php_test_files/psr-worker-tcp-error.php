<?php

require __DIR__ . '/vendor/autoload.php';

use Spiral\RoadRunner\Worker;
use Spiral\RoadRunner\Tcp\TcpWorker;
use Spiral\RoadRunner\Tcp\TcpEvent;

// Answers every data packet, except the "fail" marker: that one is reported as a
// worker error, which the pool hands back to the plugin as a SoftJob error.
$worker = Worker::create();

$tcpWorker = new TcpWorker($worker);

while ($request = $tcpWorker->waitRequest()) {
    if ($request->getEvent() === TcpEvent::Data) {
        if (trim($request->getBody()) === 'fail') {
            $worker->error('tcp worker failed to handle the payload');
            continue;
        }

        $tcpWorker->respond("pong\r\n");
        continue;
    }

    // stay silent on CONNECTED and CLOSE, keep reading
    $tcpWorker->read();
}
