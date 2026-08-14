<?php

require __DIR__ . '/vendor/autoload.php';

use Spiral\RoadRunner\Worker;
use Spiral\RoadRunner\Tcp\TcpWorker;
use Spiral\RoadRunner\Tcp\TcpResponse;
use Spiral\RoadRunner\Tcp\TcpEvent;

// Answers the first data packet with a body and asks RoadRunner to close the
// connection right after it has been written (the WRITECLOSE branch).
$worker = Worker::create();

$tcpWorker = new TcpWorker($worker);

while ($request = $tcpWorker->waitRequest()) {
    if ($request->getEvent() === TcpEvent::Data) {
        $tcpWorker->respond("goodbye\r\n", TcpResponse::RespondClose);
        continue;
    }

    // stay silent on CONNECTED and CLOSE, keep reading
    $tcpWorker->read();
}
