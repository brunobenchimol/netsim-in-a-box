import React from "react";
import TcErrorBoundary from "../components/TcErrorBoundary";
import {Accordion, Container, Form, Row, Col, Button} from "react-bootstrap";
import Scan from "./Scan";
import NetFilter from "../components/NetFilter";
import {TcConfigQuery} from "../components/TcConfigQuery";
import axios from "axios";
import {useErrorHandler} from "react-error-boundary";
import {SimpleStrategyStorage} from "../utils";
import {StrategySetting} from "../components/StrategySetting";

export default function SingleStategy() {
  const [scanPanels, setScanPanels] = React.useState([0]);
  const [executing, setExecuting] = React.useState(false);
  const [gIfaces, setGIfaces] = React.useState();
  const handleError = useErrorHandler();

  // Update whether it has ifb interface.
  React.useEffect(() => {
    if (!setGIfaces) return;

    axios.get('/tc/api/v1/init').then(res => {
      const data = res?.data?.data;
      console.log(`TC: Init ok, ${JSON.stringify(data)}`);
      setGIfaces(data?.ifaces);
    }).catch(handleError);
  }, [handleError, setGIfaces]);

  // For callback to update state, because in callback we can only get the copy, so we need a ref to point to the latest
  // copy of state of variant objects.
  const ref = React.useRef({});
  React.useEffect(() => {
    ref.current.scanPanels = scanPanels;
  }, [scanPanels]);

  const appendNewScan = React.useCallback(() => {
    setScanPanels([ref.current.scanPanels.length, ...ref.current.scanPanels]);
  }, [setScanPanels, ref]);

  // Load filter and strategy from storage.
  const defaultFilter = SimpleStrategyStorage.loadFilter() || {};
  const defaultStrategy = SimpleStrategyStorage.loadStrategy() || {};
  console.log(`load filter=${JSON.stringify(defaultFilter)}, strategy=${JSON.stringify(defaultStrategy)}`);

  return <TcErrorBoundary>
    <Container fluid={true}>
      <TcErrorBoundary>
        <SingleStategySetting defaultFilter={defaultFilter} defaultStrategy={defaultStrategy}/>
        <p/>
        {scanPanels?.length && scanPanels.map(e => {
          return <React.Fragment key={e}>
            <Scan
              appendNewScan={appendNewScan}
              executing={executing}
              setExecuting={setExecuting}
              gIfaces={gIfaces}
            />
            <p/>
          </React.Fragment>;
        })}
      </TcErrorBoundary>
    </Container>
  </TcErrorBoundary>;
}

function SingleStategySetting({defaultFilter, defaultStrategy}) {
  const [executing, setExecuting] = React.useState(false);
  const [refresh, setRefresh] = React.useState(0);
  const [validated, setValidated] = React.useState(false);
  const handleError = useErrorHandler();

  const [iface, setIface] = React.useState(defaultFilter.iface);
  const [protocol, setProtocol] = React.useState(defaultFilter.protocol || 'ip');
  const [direction, setDirection] = React.useState(defaultFilter.direction || 'incoming');
  const [identifyKey, setIdentifyKey] = React.useState(defaultFilter.identifyKey || 'all');
  const [identifyValue, setIdentifyValue] = React.useState(defaultFilter.identifyValue);
  const [strategy, setStrategy] = React.useState(defaultStrategy.strategy || 'loss');
  const [loss, setLoss] = React.useState(defaultStrategy.loss || '1');
  const [delay, setDelay] = React.useState(defaultStrategy.delay || '1');
  const [rate, setRate] = React.useState(defaultStrategy.rate || '1000000');
  const [delayDistro, setDelayDistro] = React.useState(defaultStrategy.delayDistro);

  const [gIfaces, setGIfaces] = React.useState();
  const [ifbs, setIfbs] = React.useState();

  // For callback to update state, because in callback we can only get the copy, so we need a ref to point to the latest
  // copy of state of variant objects.
  const ref = React.useRef({});
  React.useEffect(() => {
    ref.current.refresh = refresh;
  }, [refresh]);

  // Update whether it has ifb interface.
  React.useEffect(() => {
    axios.get('/tc/api/v1/init').then(res => {
      const data = res?.data?.data;
      console.log(`TC: Init ok, ${JSON.stringify(data)}`);

      const ifaces = data?.ifaces;
      setGIfaces(ifaces);

      const ifbs = ifaces?.filter(e => e?.name?.indexOf('ifb') >= 0);
      setIfbs(ifbs);
    });
  }, [refresh, setIfbs, setGIfaces]);

  // Reset the TC config.
  const resetNetwork = React.useCallback((e) => {
    if (!iface) {
      setValidated(true);
      return;
    }

    setExecuting(true);
    axios.get(`/tc/api/v1/config/reset?iface=${iface}`).then(res => {
      const conf = res?.data?.data;
      console.log(`query ok, iface=${iface}, conf=${JSON.stringify(conf)}`);
    }).catch(handleError).finally(async () => {
      setRefresh(ref.current.refresh + 1);
      await new Promise(resolve => setTimeout(resolve, 300));
      setExecuting(false);
    });
  }, [iface, handleError, setExecuting, ref, setRefresh]);

  // Setup the TC config.
  const setupNetwork = React.useCallback(() => {
    if (!iface || !protocol || (identifyKey !== 'all' && !identifyValue)) {
      setValidated(true);
      return;
    }
    if (!strategy || strategy === 'no') {
      return alert('请选择策略');
    }
    if (delayDistro && Number(delayDistro) > Number(delay)) {
      return alert(`延迟抖动${delayDistro}不能大于延迟${delay}`);
    }

    SimpleStrategyStorage.saveFilter(iface, protocol, direction, identifyKey, identifyValue);
    SimpleStrategyStorage.saveStrategy(strategy, loss, delay, rate, delayDistro);
    console.log(`save iface=${iface}, protocol=${protocol}, direction=${direction}, identify=${identifyKey}/${identifyValue}, strategy=${strategy}, loss=${loss}, delay=${delay}, rate=${rate}, delayDistro=${delayDistro}`);

    setExecuting(true);
    const queries = [
      iface ? `iface=${iface}` : null,
      protocol ? `protocol=${protocol}` : null,
      direction ? `direction=${direction}` : null,
      identifyKey ? `identifyKey=${identifyKey}` : null,
      identifyValue ? `identifyValue=${identifyValue}` : null,
      strategy && strategy !== 'no' ? `strategy=${strategy}` : null,
      loss && strategy === 'loss' ? `loss=${loss}` : null,
      delay && strategy === 'delay' ? `delay=${delay}` : null,
      rate && strategy === 'rate' ? `rate=${rate}` : null,
      delayDistro && strategy === 'delay' ? `delayDistro=${delayDistro}` : null,
      `api=${window.location.port}`,
    ].filter(e => e);
    axios.get(`/tc/api/v1/config/setup?${queries.join('&')}`).then(res => {
      const conf = res?.data?.data;
      console.log(`query ok, ${queries.join(', ')}, conf=${JSON.stringify(conf)}`);
    }).catch(handleError).finally(async () => {
      setRefresh(ref.current.refresh + 1);
      await new Promise(resolve => setTimeout(resolve, 300));
      setExecuting(false);
    });
  }, [
    iface, protocol, direction, identifyKey, identifyValue,
    strategy, loss, delay, rate, delayDistro,
    handleError, setExecuting, ref, setRefresh,
  ]);

  return <Accordion defaultActiveKey="0">
    <Accordion.Item eventKey="0">
      <Accordion.Header>网络丢包设置</Accordion.Header>
      <Accordion.Body>
        <Form noValidate validated={validated}>
          <Row>
            <Col xs='auto'>
              {gIfaces && <NetFilter gIfaces={gIfaces}
                         iface={iface} setIface={setIface} protocol={protocol} setProtocol={setProtocol}
                         direction={direction} setDirection={setDirection} identifyKey={identifyKey}
                         setIdentifyKey={setIdentifyKey} identifyValue={identifyValue}
                         setIdentifyValue={setIdentifyValue}/>}
            </Col>
          </Row>
          <Row>
            <Col xs='auto'>
              <StrategySetting strategy={strategy} setStrategy={setStrategy} loss={loss}
                               setLoss={setLoss} delay={delay} setDelay={setDelay}
                               rate={rate} setRate={setRate} delayDistro={delayDistro}
                               setDelayDistro={setDelayDistro}/>
            </Col>
          </Row>
          <Row>
            <Col xs='auto'>
              <TcErrorBoundary>
                <Button variant="primary" type="button" onClick={setupNetwork} disabled={executing}>
                  设置网络
                </Button> &nbsp;
                <Button variant="primary" type="button" onClick={resetNetwork} disabled={executing}>
                  重置网络
                </Button>
              </TcErrorBoundary>
            </Col>
          </Row>
          <Row>
            {iface && <Col xs='auto'>
              <p/>
              <TcConfigQuery iface={iface} forceRefresh={refresh}/>
            </Col>}
            {ifbs && ifbs?.filter(e => e.name).map(e => {
              return <Col xs='auto' key={e.name}>
                <p/>
                <TcConfigQuery iface={e.name} forceRefresh={refresh}/>
              </Col>;
            })}
          </Row>
        </Form>
      </Accordion.Body>
    </Accordion.Item>
  </Accordion>;
}
